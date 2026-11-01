use super::super::FileIdentity;
use cap_std::fs::{Dir, Metadata, MetadataExt};
use std::{fs::File, io, os::unix::fs::PermissionsExt};

pub const REPLACE_REQUIRES_BACKUP: bool = false;
pub fn exchange(from: &Dir, name: &str, to: &Dir, target: &str) -> io::Result<()> {
    rustix::fs::renameat_with(from, name, to, target, rustix::fs::RenameFlags::EXCHANGE)
        .map_err(Into::into)
}

pub fn identity(metadata: &Metadata) -> io::Result<FileIdentity> {
    Ok(FileIdentity {
        volume: metadata.dev(),
        index: (metadata.ino() as u128).to_le_bytes(),
    })
}
pub fn dangerous(metadata: &Metadata) -> bool {
    metadata.file_type().is_symlink() || (!metadata.is_file() && !metadata.is_dir())
}
pub fn mode(metadata: &Metadata) -> u32 {
    metadata.mode() & 0o777
}
pub fn set_mode(file: &File, mode: u32) -> io::Result<()> {
    file.set_permissions(std::fs::Permissions::from_mode(mode))
}
pub fn sync_dir(dir: &Dir) -> io::Result<()> {
    dir.try_clone()?.into_std_file().sync_all()
}
pub fn rename_no_replace(from: &Dir, name: &str, to: &Dir, target: &str) -> io::Result<()> {
    rustix::fs::renameat_with(from, name, to, target, rustix::fs::RenameFlags::NOREPLACE)
        .map_err(Into::into)
}
pub fn rename_replace(from: &Dir, name: &str, to: &Dir, target: &str) -> io::Result<()> {
    rustix::fs::renameat(from, name, to, target).map_err(Into::into)
}

pub fn identity_at(dir: &Dir, name: impl AsRef<std::path::Path>) -> io::Result<FileIdentity> {
    identity(&dir.symlink_metadata(name)?)
}
pub fn identity_dir(dir: &Dir) -> io::Result<FileIdentity> {
    identity(&dir.dir_metadata()?)
}
pub fn identity_file(file: &cap_std::fs::File) -> io::Result<FileIdentity> {
    identity(&file.metadata()?)
}
