use super::super::FileIdentity;
use cap_fs_ext::{FollowSymlinks, OpenOptionsFollowExt};
use cap_std::fs::{Dir, Metadata, MetadataExt, OpenOptions, OpenOptionsExt};
use std::{
    fs::File,
    io, mem,
    os::windows::io::{AsRawHandle, FromRawHandle},
    ptr,
};
use windows_sys::Wdk::Foundation::OBJECT_ATTRIBUTES;
use windows_sys::Wdk::Storage::FileSystem::{
    FILE_CREATE, FILE_NON_DIRECTORY_FILE, FILE_OPEN_REPARSE_POINT, FILE_RENAME_INFORMATION,
    FILE_SYNCHRONOUS_IO_NONALERT, FileRenameInformation, NtCreateFile, NtSetInformationFile,
};
use windows_sys::Win32::Foundation::{
    CloseHandle, GENERIC_READ, GENERIC_WRITE, LocalFree, OBJ_CASE_INSENSITIVE,
    RtlNtStatusToDosError, UNICODE_STRING,
};
use windows_sys::Win32::Security::Authorization::{
    ConvertSidToStringSidW, ConvertStringSecurityDescriptorToSecurityDescriptorW, GetSecurityInfo,
    SDDL_REVISION_1, SE_FILE_OBJECT,
};
use windows_sys::Win32::Security::{
    ACE_HEADER, ACL, CreateWellKnownSid, DACL_SECURITY_INFORMATION, EqualSid, GetAce,
    GetSecurityDescriptorControl, GetTokenInformation, IsValidSid, OWNER_SECURITY_INFORMATION,
    PSECURITY_DESCRIPTOR, PSID, SE_DACL_PROTECTED, TOKEN_QUERY, TOKEN_USER, TokenUser,
    WinLocalSystemSid,
};
use windows_sys::Win32::Storage::FileSystem::*;
use windows_sys::Win32::System::IO::IO_STATUS_BLOCK;
use windows_sys::Win32::System::Threading::{GetCurrentProcess, OpenProcessToken};

struct LocalAllocation(*mut core::ffi::c_void);
impl Drop for LocalAllocation {
    fn drop(&mut self) {
        if !self.0.is_null() {
            unsafe {
                LocalFree(self.0);
            }
        }
    }
}

// Keep TOKEN_USER and its referenced SID in one suitably aligned allocation.
struct UserSid(Vec<usize>);
impl UserSid {
    fn current() -> io::Result<Self> {
        let mut token = ptr::null_mut();
        if unsafe { OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &mut token) } == 0 {
            return Err(io::Error::last_os_error());
        }
        let mut size = 0;
        unsafe {
            GetTokenInformation(token, TokenUser, ptr::null_mut(), 0, &mut size);
        }
        if size < mem::size_of::<TOKEN_USER>() as u32 {
            let error = io::Error::last_os_error();
            unsafe {
                CloseHandle(token);
            }
            return Err(error);
        }
        let mut buffer = vec![0usize; (size as usize).div_ceil(mem::size_of::<usize>())];
        let ok = unsafe {
            GetTokenInformation(
                token,
                TokenUser,
                buffer.as_mut_ptr().cast(),
                size,
                &mut size,
            )
        };
        let error = if ok == 0 {
            Some(io::Error::last_os_error())
        } else {
            None
        };
        unsafe {
            CloseHandle(token);
        }
        if let Some(error) = error {
            return Err(error);
        }
        let user = Self(buffer);
        if user.sid().is_null() || unsafe { IsValidSid(user.sid()) } == 0 {
            return Err(io::Error::other("invalid current user SID"));
        }
        Ok(user)
    }
    fn sid(&self) -> PSID {
        unsafe { (*(self.0.as_ptr().cast::<TOKEN_USER>())).User.Sid }
    }
    fn string(&self) -> io::Result<String> {
        let mut text = ptr::null_mut();
        if unsafe { ConvertSidToStringSidW(self.sid(), &mut text) } == 0 {
            return Err(io::Error::last_os_error());
        }
        let allocation = LocalAllocation(text.cast());
        let mut len = 0;
        while unsafe { *text.add(len) } != 0 {
            len += 1;
        }
        let result = String::from_utf16(unsafe { std::slice::from_raw_parts(text, len) })
            .map_err(|_| io::Error::other("invalid SID string"));
        drop(allocation);
        result
    }
}

fn private_descriptor(user: &UserSid) -> io::Result<LocalAllocation> {
    let sid = user.string()?;
    let sddl: Vec<u16> = format!("O:{sid}D:P(A;;FA;;;{sid})(A;;FA;;;SY)\0")
        .encode_utf16()
        .collect();
    let mut descriptor: PSECURITY_DESCRIPTOR = ptr::null_mut();
    if unsafe {
        ConvertStringSecurityDescriptorToSecurityDescriptorW(
            sddl.as_ptr(),
            SDDL_REVISION_1,
            &mut descriptor,
            ptr::null_mut(),
        )
    } == 0
    {
        return Err(io::Error::last_os_error());
    }
    Ok(LocalAllocation(descriptor))
}

/// Create relative to the already verified directory with a protected DACL at creation.
pub fn create_private_file(dir: &Dir, name: &str) -> io::Result<File> {
    let user = UserSid::current()?;
    let descriptor = private_descriptor(&user)?;
    let wide: Vec<u16> = name.encode_utf16().collect();
    let length = wide
        .len()
        .checked_mul(2)
        .and_then(|n| u16::try_from(n).ok())
        .ok_or_else(|| io::Error::other("private file name too long"))?;
    let filename = UNICODE_STRING {
        Length: length,
        MaximumLength: length,
        Buffer: wide.as_ptr().cast_mut(),
    };
    let attributes = OBJECT_ATTRIBUTES {
        Length: mem::size_of::<OBJECT_ATTRIBUTES>() as u32,
        RootDirectory: dir.as_raw_handle(),
        ObjectName: &filename,
        Attributes: OBJ_CASE_INSENSITIVE,
        SecurityDescriptor: descriptor.0.cast(),
        SecurityQualityOfService: ptr::null(),
    };
    let mut handle = ptr::null_mut();
    let mut status_block = IO_STATUS_BLOCK::default();
    let status = unsafe {
        NtCreateFile(
            &mut handle,
            GENERIC_READ | GENERIC_WRITE | READ_CONTROL | DELETE | SYNCHRONIZE,
            &attributes,
            &mut status_block,
            ptr::null(),
            FILE_ATTRIBUTE_NORMAL,
            FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE,
            FILE_CREATE,
            FILE_NON_DIRECTORY_FILE | FILE_SYNCHRONOUS_IO_NONALERT | FILE_OPEN_REPARSE_POINT,
            ptr::null(),
            0,
        )
    };
    if status < 0 {
        return Err(io::Error::from_raw_os_error(
            unsafe { RtlNtStatusToDosError(status) } as i32,
        ));
    }
    let file = unsafe { File::from_raw_handle(handle) };
    verify_private_file(&file)?;
    Ok(file)
}

/// Validate owner and each grant on the same handle used to read/write the secret.
pub fn verify_private_file(file: &impl AsRawHandle) -> io::Result<()> {
    let user = UserSid::current()?;
    let mut system = [0usize; 16];
    let mut system_size = mem::size_of_val(&system) as u32;
    let system_sid = system.as_mut_ptr().cast();
    if unsafe {
        CreateWellKnownSid(
            WinLocalSystemSid,
            ptr::null_mut(),
            system_sid,
            &mut system_size,
        )
    } == 0
    {
        return Err(io::Error::last_os_error());
    }
    let mut owner: PSID = ptr::null_mut();
    let mut acl: *mut ACL = ptr::null_mut();
    let mut descriptor: PSECURITY_DESCRIPTOR = ptr::null_mut();
    let result = unsafe {
        GetSecurityInfo(
            file.as_raw_handle(),
            SE_FILE_OBJECT,
            OWNER_SECURITY_INFORMATION | DACL_SECURITY_INFORMATION,
            &mut owner,
            ptr::null_mut(),
            &mut acl,
            ptr::null_mut(),
            &mut descriptor,
        )
    };
    if result != 0 {
        return Err(io::Error::from_raw_os_error(result as i32));
    }
    let allocation = LocalAllocation(descriptor);
    let invalid = || {
        io::Error::new(
            io::ErrorKind::PermissionDenied,
            "private file ACL is not restricted to the current user and SYSTEM",
        )
    };
    let mut control = 0;
    let mut revision = 0;
    if owner.is_null()
        || acl.is_null()
        || descriptor.is_null()
        || unsafe { EqualSid(owner, user.sid()) } == 0
        || unsafe { GetSecurityDescriptorControl(descriptor, &mut control, &mut revision) } == 0
        || control & SE_DACL_PROTECTED == 0
        || unsafe { (*acl).AceCount } != 2
    {
        return Err(invalid());
    }
    let mut found_user = false;
    let mut found_system = false;
    for index in 0..2 {
        let mut raw = ptr::null_mut();
        if unsafe { GetAce(acl, index, &mut raw) } == 0 || raw.is_null() {
            return Err(invalid());
        }
        let offset = (raw as usize)
            .checked_sub(acl as usize)
            .ok_or_else(invalid)?;
        let acl_size = unsafe { (*acl).AclSize as usize };
        if offset
            .checked_add(mem::size_of::<ACE_HEADER>())
            .is_none_or(|end| end > acl_size)
        {
            return Err(invalid());
        }
        let header = unsafe { &*raw.cast::<ACE_HEADER>() };
        // Only explicit allow ACEs are accepted; reject unknown ACE formats and inherited grants.
        if header.AceType != 0
            || header.AceFlags != 0
            || (header.AceSize as usize) < 16
            || offset
                .checked_add(header.AceSize as usize)
                .is_none_or(|end| end > acl_size)
        {
            return Err(invalid());
        }
        let sid: PSID = unsafe { raw.cast::<u8>().add(8).cast() };
        let sid_bytes = unsafe { sid.cast::<u8>().add(1).read() as usize };
        if 8usize
            .checked_add(sid_bytes.saturating_mul(4))
            .is_none_or(|n| n + 8 > header.AceSize as usize)
            || unsafe { IsValidSid(sid) } == 0
        {
            return Err(invalid());
        }
        if unsafe { EqualSid(sid, user.sid()) } != 0 {
            found_user = true;
        } else if unsafe { EqualSid(sid, system_sid) } != 0 {
            found_system = true;
        } else {
            return Err(invalid());
        }
    }
    drop(allocation);
    if !found_user || !found_system {
        return Err(invalid());
    }
    Ok(())
}

pub const REPLACE_REQUIRES_BACKUP: bool = true;
pub fn exchange(_from: &Dir, _name: &str, _to: &Dir, _target: &str) -> io::Result<()> {
    Err(io::Error::new(
        io::ErrorKind::Unsupported,
        "Windows replacement uses recorded backup and publish steps",
    ))
}

fn identity_handle(file: &impl AsRawHandle) -> io::Result<FileIdentity> {
    let mut info: FILE_ID_INFO = unsafe { mem::zeroed() };
    // FILE_ID_INFO preserves all 128 bits, including ReFS identities.
    if unsafe {
        GetFileInformationByHandleEx(
            file.as_raw_handle(),
            FileIdInfo,
            (&mut info as *mut FILE_ID_INFO).cast(),
            mem::size_of::<FILE_ID_INFO>() as u32,
        )
    } == 0
    {
        return Err(io::Error::last_os_error());
    }
    Ok(FileIdentity {
        volume: info.VolumeSerialNumber,
        index: info.FileId.Identifier,
    })
}
pub fn identity_at(dir: &Dir, name: impl AsRef<std::path::Path>) -> io::Result<FileIdentity> {
    let mut options = OpenOptions::new();
    options
        .read(true)
        .follow(FollowSymlinks::No)
        .access_mode(FILE_READ_ATTRIBUTES)
        .share_mode(FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE)
        .custom_flags(FILE_FLAG_BACKUP_SEMANTICS | FILE_FLAG_OPEN_REPARSE_POINT);
    identity_handle(&dir.open_with(name, &options)?)
}
pub fn identity_dir(dir: &Dir) -> io::Result<FileIdentity> {
    identity_handle(dir)
}
pub fn identity_file(file: &cap_std::fs::File) -> io::Result<FileIdentity> {
    identity_handle(file)
}
pub fn identity_std_file(file: &File) -> io::Result<FileIdentity> {
    identity_handle(file)
}
pub fn dangerous(metadata: &Metadata) -> bool {
    metadata.file_attributes() & FILE_ATTRIBUTE_REPARSE_POINT != 0
        || (!metadata.is_file() && !metadata.is_dir())
}
pub fn mode(metadata: &Metadata) -> u32 {
    if metadata.permissions().readonly() {
        0o444
    } else {
        0o644
    }
}
pub fn set_mode(file: &File, mode: u32) -> io::Result<()> {
    let mut permissions = file.metadata()?.permissions();
    permissions.set_readonly(mode & 0o222 == 0);
    file.set_permissions(permissions)
}
// Windows has no supported directory flush with Unix fsync semantics.
pub fn sync_dir(_dir: &Dir) -> io::Result<()> {
    Ok(())
}
pub fn rename_no_replace(from: &Dir, name: &str, to: &Dir, target: &str) -> io::Result<()> {
    rename(from, name, to, target, false)
}
pub fn rename_replace(from: &Dir, name: &str, to: &Dir, target: &str) -> io::Result<()> {
    rename(from, name, to, target, true)
}
fn rename(from: &Dir, name: &str, to: &Dir, target: &str, replace: bool) -> io::Result<()> {
    let mut options = OpenOptions::new();
    options
        .read(true)
        .follow(FollowSymlinks::No)
        .access_mode(DELETE | FILE_READ_ATTRIBUTES | SYNCHRONIZE)
        .share_mode(FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE)
        .custom_flags(FILE_FLAG_BACKUP_SEMANTICS);
    let file = from.open_with(name, &options)?;
    if dangerous(&file.metadata()?) {
        return Err(io::Error::other("reparse point rejected"));
    }
    let target: Vec<u16> = target.encode_utf16().collect();
    let bytes = mem::size_of::<FILE_RENAME_INFORMATION>() + target.len() * 2;
    let mut storage = vec![0usize; bytes.div_ceil(mem::size_of::<usize>())];
    let mut status_block = IO_STATUS_BLOCK::default();
    // The NT call keeps the target relative to the held directory handle.
    unsafe {
        let info = storage.as_mut_ptr().cast::<FILE_RENAME_INFORMATION>();
        (*info).Anonymous.ReplaceIfExists = replace;
        (*info).RootDirectory = to.as_raw_handle();
        (*info).FileNameLength = (target.len() * 2) as u32;
        std::ptr::copy_nonoverlapping(target.as_ptr(), (*info).FileName.as_mut_ptr(), target.len());
        let status = NtSetInformationFile(
            file.as_raw_handle(),
            &mut status_block,
            info.cast(),
            bytes as u32,
            FileRenameInformation,
        );
        if status < 0 {
            return Err(io::Error::from_raw_os_error(
                RtlNtStatusToDosError(status) as i32
            ));
        }
    }
    Ok(())
}
