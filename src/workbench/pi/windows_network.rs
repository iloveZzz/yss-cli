use std::io;
use std::net::{Ipv4Addr, Ipv6Addr, SocketAddr, SocketAddrV6, TcpListener, TcpStream};
use std::ptr::{self, NonNull};
use std::time::{Duration, Instant};
use windows_sys::Win32::Foundation::{ERROR_NOT_FOUND, NO_ERROR};
use windows_sys::Win32::NetworkManagement::IpHelper::{
    FreeMibTable, GetUnicastIpAddressTable, MIB_UNICASTIPADDRESS_ROW, MIB_UNICASTIPADDRESS_TABLE,
};
use windows_sys::Win32::Networking::WinSock::{
    AF_INET, AF_INET6, AF_UNSPEC, IpDadStateDeprecated, IpDadStatePreferred,
};

const CONNECT_TIMEOUT: Duration = Duration::from_millis(350);
const FAMILY_TIMEOUT: Duration = Duration::from_secs(2);

struct AddressTable(NonNull<MIB_UNICASTIPADDRESS_TABLE>);

impl Drop for AddressTable {
    fn drop(&mut self) {
        // SAFETY: GetUnicastIpAddressTable transferred ownership of this allocation.
        unsafe { FreeMibTable(self.0.as_ptr().cast()) };
    }
}

fn local_addresses() -> io::Result<Vec<(bool, SocketAddr)>> {
    let mut raw = ptr::null_mut();
    // SAFETY: The out pointer is initialized and writable. AF_UNSPEC requests both families.
    match unsafe { GetUnicastIpAddressTable(AF_UNSPEC, &mut raw) } {
        NO_ERROR => {}
        ERROR_NOT_FOUND => return Ok(Vec::new()),
        error => {
            return Err(io::Error::other(format!(
                "无法读取 Windows 本机 IP 地址表：{}",
                io::Error::from_raw_os_error(error as i32)
            )));
        }
    }
    let table = AddressTable(
        NonNull::new(raw).ok_or_else(|| io::Error::other("Windows 本机 IP 地址表返回了空指针"))?,
    );
    let raw = table.0.as_ptr();
    // SAFETY: On success the API supplies the header followed by NumEntries rows.
    let count = unsafe { ptr::addr_of!((*raw).NumEntries).read() } as usize;
    if count == 0 {
        return Ok(Vec::new());
    }
    if count > isize::MAX as usize / size_of::<MIB_UNICASTIPADDRESS_ROW>() {
        return Err(io::Error::other("Windows 本机 IP 地址表过大"));
    }
    // SAFETY: Table's native offset includes any padding after the header. Keep its allocation
    // alive until the slice has been converted into owned addresses.
    let rows = unsafe {
        std::slice::from_raw_parts(
            ptr::addr_of!((*raw).Table).cast::<MIB_UNICASTIPADDRESS_ROW>(),
            count,
        )
    };
    let mut addresses: Vec<_> = rows
        .iter()
        .filter(|row| {
            row.ValidLifetime > 0
                && (row.DadState == IpDadStatePreferred || row.DadState == IpDadStateDeprecated)
        })
        .filter_map(|row| {
            address_from_row(row).map(|addr| (row.DadState == IpDadStateDeprecated, addr))
        })
        .collect();
    // Try preferred addresses first. Deprecated addresses are still assigned and valid.
    addresses.sort_unstable();
    addresses.dedup();
    Ok(addresses)
}

fn address_from_row(row: &MIB_UNICASTIPADDRESS_ROW) -> Option<SocketAddr> {
    // SAFETY: si_family is the common discriminant for the two sockaddr layouts.
    unsafe {
        match row.Address.si_family {
            AF_INET => {
                let ip = Ipv4Addr::from(row.Address.Ipv4.sin_addr.S_un.S_addr.to_ne_bytes());
                if ip.is_loopback() || ip.is_unspecified() || ip.is_multicast() || ip.is_broadcast()
                {
                    return None;
                }
                Some(SocketAddr::from((ip, 0)))
            }
            AF_INET6 => {
                let ip = Ipv6Addr::from(row.Address.Ipv6.sin6_addr.u.Byte);
                if ip.is_loopback()
                    || ip.is_unspecified()
                    || ip.is_multicast()
                    || ip.to_ipv4_mapped().is_some()
                {
                    return None;
                }
                let scope = if ip.is_unicast_link_local() {
                    row.InterfaceIndex
                } else if ip.segments()[0] & 0xffc0 == 0xfec0 {
                    // SCOPE_ID stores the zone in its low 28 bits.
                    row.ScopeId.Anonymous.Value & 0x0fff_ffff
                } else {
                    0
                };
                Some(SocketAddr::V6(SocketAddrV6::new(ip, 0, 0, scope)))
            }
            _ => None,
        }
    }
}

fn verified_listener(mut address: SocketAddr, timeout: Duration) -> io::Result<TcpListener> {
    let deadline = Instant::now() + timeout;
    let listener = TcpListener::bind(address)?;
    listener.set_nonblocking(true)?;
    let endpoint = listener.local_addr()?;
    address.set_port(endpoint.port());
    if endpoint != address {
        return Err(io::Error::other("监听地址或 IPv6 范围与绑定请求不一致"));
    }
    let remaining = deadline.saturating_duration_since(Instant::now());
    if remaining.is_zero() {
        return Err(io::Error::new(io::ErrorKind::TimedOut, "监听器绑定超时"));
    }
    let client = TcpStream::connect_timeout(&endpoint, remaining)?;
    let source = client.local_addr()?;
    loop {
        match listener.accept() {
            Ok((_, peer)) if peer.ip() == source.ip() && peer.port() == source.port() => {
                return Ok(listener);
            }
            Ok(_) => return Err(io::Error::other("监听器收到非探测连接")),
            Err(error) if error.kind() == io::ErrorKind::WouldBlock => {
                if Instant::now() >= deadline {
                    return Err(io::Error::new(
                        io::ErrorKind::TimedOut,
                        "未收到宿主探测连接",
                    ));
                }
                std::thread::sleep(Duration::from_millis(5));
            }
            Err(error) => return Err(error),
        }
    }
}

pub(super) fn listeners() -> io::Result<Vec<TcpListener>> {
    let mut sockets = Vec::with_capacity(4);
    let mut loopback_errors = Vec::new();
    for address in [
        SocketAddr::from((Ipv4Addr::LOCALHOST, 0)),
        SocketAddr::from((Ipv6Addr::LOCALHOST, 0)),
    ] {
        match verified_listener(address, CONNECT_TIMEOUT) {
            Ok(listener) => sockets.push(listener),
            Err(error) => loopback_errors.push(format!("{address}：{error}")),
        }
    }
    if sockets.is_empty() {
        return Err(io::Error::other(format!(
            "无法验证 Windows 回环隔离探测端点：{}",
            loopback_errors.join("；")
        )));
    }

    let addresses = local_addresses()?;
    for (ipv4, family) in [(true, "IPv4"), (false, "IPv6")] {
        let mut candidates = addresses
            .iter()
            .map(|&(_, addr)| addr)
            .filter(|addr| addr.is_ipv4() == ipv4)
            .peekable();
        if candidates.peek().is_none() {
            continue;
        }
        let deadline = Instant::now() + FAMILY_TIMEOUT;
        let mut result = Err(io::Error::from(io::ErrorKind::TimedOut));
        for address in candidates {
            let timeout = deadline
                .saturating_duration_since(Instant::now())
                .min(CONNECT_TIMEOUT);
            if timeout.is_zero() {
                break;
            }
            result = verified_listener(address, timeout);
            if result.is_ok() {
                break;
            }
        }
        sockets.push(result.map_err(|error| {
            io::Error::other(format!(
                "无法验证 Windows {family} 非回环隔离探测端点：{error}"
            ))
        })?);
    }
    Ok(sockets)
}
