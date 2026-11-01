use super::super::FileIdentity;
use cap_fs_ext::{FollowSymlinks, OpenOptionsFollowExt};
use cap_std::fs::{Dir, Metadata, MetadataExt, OpenOptions, OpenOptionsExt};
use std::{fs::File, io, mem, os::windows::io::AsRawHandle};
use windows_sys::Wdk::Storage::FileSystem::{
    FILE_RENAME_INFORMATION, FileRenameInformation, NtSetInformationFile,
};
use windows_sys::Win32::Foundation::RtlNtStatusToDosError;
use windows_sys::Win32::Storage::FileSystem::*;
use windows_sys::Win32::System::IO::IO_STATUS_BLOCK;

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
