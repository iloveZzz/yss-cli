use crate::{
    error::{ErrorCode, Result, YssError},
    model::{MAX_ASSET, RelPath},
};
use flate2::{Compression, GzBuilder, bufread::GzDecoder};
use std::{
    collections::BTreeMap,
    io::{Cursor, Read},
};

pub(crate) struct Entry {
    pub bytes: Vec<u8>,
    pub mode: u32,
}
fn invalid() -> YssError {
    YssError::new(ErrorCode::InvalidInput, "归档结构、路径或长度不合法")
}
pub(crate) fn pack(entries: &BTreeMap<RelPath, Entry>) -> Result<Vec<u8>> {
    let total: u64 = entries
        .values()
        .map(|e| (e.bytes.len() as u64).div_ceil(512) * 512 + 512)
        .sum();
    if total + 1024 > MAX_ASSET {
        return Err(invalid());
    }
    let gzip = GzBuilder::new()
        .mtime(0)
        .operating_system(255)
        .write(Vec::new(), Compression::best());
    let mut archive = tar::Builder::new(gzip);
    for (path, entry) in entries {
        let mut header = tar::Header::new_ustar();
        header.set_size(entry.bytes.len() as u64);
        header.set_mode(entry.mode);
        header.set_uid(0);
        header.set_gid(0);
        header.set_mtime(0);
        header.set_entry_type(tar::EntryType::Regular);
        header.set_cksum();
        archive.append_data(&mut header, path.as_str(), entry.bytes.as_slice())?;
    }
    let bytes = archive.into_inner()?.finish()?;
    if bytes.len() as u64 > MAX_ASSET {
        return Err(invalid());
    }
    Ok(bytes)
}
pub(crate) fn unpack(bytes: &[u8]) -> Result<BTreeMap<RelPath, Entry>> {
    if bytes.len() as u64 > MAX_ASSET {
        return Err(invalid());
    }
    let mut decoder = GzDecoder::new(bytes);
    let mut raw = Vec::new();
    (&mut decoder)
        .take(MAX_ASSET + 1)
        .read_to_end(&mut raw)
        .map_err(|_| invalid())?;
    if raw.len() as u64 > MAX_ASSET || !decoder.into_inner().is_empty() || raw.len() % 512 != 0 {
        return Err(invalid());
    }
    let mut archive = tar::Archive::new(Cursor::new(&raw));
    let mut entries = BTreeMap::new();
    for entry in archive.entries().map_err(|_| invalid())?.raw(true) {
        let mut entry = entry.map_err(|_| invalid())?;
        if !entry.header().entry_type().is_file() || entry.size() > MAX_ASSET {
            return Err(invalid());
        }
        let path = RelPath::new(std::str::from_utf8(&entry.path_bytes()).map_err(|_| invalid())?)?;
        if entries.contains_key(&path) {
            return Err(invalid());
        }
        let mode = entry.header().mode().map_err(|_| invalid())?;
        if !matches!(mode, 0o644 | 0o755) {
            return Err(invalid());
        }
        let mut bytes = Vec::new();
        entry.read_to_end(&mut bytes).map_err(|_| invalid())?;
        if bytes.len() as u64 != entry.size() {
            return Err(invalid());
        }
        entries.insert(path, Entry { bytes, mode });
    }
    let end = archive.into_inner().position() as usize;
    if raw.len().saturating_sub(end) != 512 || raw[end..].iter().any(|b| *b != 0) {
        return Err(invalid());
    }
    Ok(entries)
}
