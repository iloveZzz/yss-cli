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
    FILE_CREATE, FILE_DIRECTORY_FILE, FILE_ID_EXTD_DIR_INFORMATION, FILE_NON_DIRECTORY_FILE,
    FILE_OPEN, FILE_OPEN_REPARSE_POINT, FILE_RENAME_INFORMATION, FILE_SYNCHRONOUS_IO_NONALERT,
    FileDispositionInformationEx, FileIdExtdDirectoryInformation, FileRenameInformation,
    NtCreateFile, NtOpenFile, NtQueryDirectoryFile, NtSetInformationFile,
};
use windows_sys::Win32::Foundation::{
    CloseHandle, GENERIC_READ, GENERIC_WRITE, LocalFree, OBJ_CASE_INSENSITIVE, OBJ_DONT_REPARSE,
    RtlNtStatusToDosError, STATUS_NO_MORE_FILES, STATUS_NO_SUCH_FILE, STATUS_SUCCESS,
    UNICODE_STRING,
};
use windows_sys::Win32::Security::Authorization::{
    ConvertSidToStringSidW, ConvertStringSecurityDescriptorToSecurityDescriptorW, GetSecurityInfo,
    SDDL_REVISION_1, SE_FILE_OBJECT,
};
use windows_sys::Win32::Security::{
    ACE_HEADER, ACL, CreateWellKnownSid, DACL_SECURITY_INFORMATION, EqualSid, GetAce,
    GetSecurityDescriptorControl, GetTokenInformation, IsValidSid, OWNER_SECURITY_INFORMATION,
    PSECURITY_DESCRIPTOR, PSID, SE_DACL_PRESENT, SE_DACL_PROTECTED, TOKEN_QUERY, TOKEN_USER,
    TokenUser, WinBuiltinAdministratorsSid, WinLocalSystemSid,
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
    descriptor_with_inheritance(user, false)
}

fn descriptor_with_inheritance(user: &UserSid, inherit: bool) -> io::Result<LocalAllocation> {
    let sid = user.string()?;
    let flags = if inherit { "OICI" } else { "" };
    let sddl: Vec<u16> = format!("O:{sid}D:P(A;{flags};FA;;;{sid})(A;{flags};FA;;;SY)\0")
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

/// Execute while a current-user/SYSTEM-only protected descriptor is alive.
pub(crate) fn with_private_descriptor<T>(
    callback: impl FnOnce(PSECURITY_DESCRIPTOR) -> io::Result<T>,
) -> io::Result<T> {
    let descriptor = private_descriptor(&UserSid::current()?)?;
    callback(descriptor.0)
}

/// Create a directory relative to a verified parent, with inheritable private ACLs at creation.
pub(crate) fn create_private_directory(dir: &Dir, name: &str) -> io::Result<File> {
    let descriptor = descriptor_with_inheritance(&UserSid::current()?, true)?;
    let wide: Vec<u16> = name.encode_utf16().collect();
    let length = wide
        .len()
        .checked_mul(2)
        .and_then(|n| u16::try_from(n).ok())
        .ok_or_else(|| io::Error::other("private directory name too long"))?;
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
    // The caller keeps this handle open while reopening the directory through
    // cap-std, which does not share DELETE access on directories.
    let status = unsafe {
        NtCreateFile(
            &mut handle,
            GENERIC_READ | GENERIC_WRITE | READ_CONTROL | SYNCHRONIZE,
            &attributes,
            &mut status_block,
            ptr::null(),
            FILE_ATTRIBUTE_DIRECTORY,
            FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE,
            FILE_CREATE,
            FILE_DIRECTORY_FILE | FILE_SYNCHRONOUS_IO_NONALERT | FILE_OPEN_REPARSE_POINT,
            ptr::null(),
            0,
        )
    };
    if status < 0 {
        return Err(io::Error::from_raw_os_error(
            unsafe { RtlNtStatusToDosError(status) } as i32,
        ));
    }
    let directory = unsafe { File::from_raw_handle(handle) };
    verify_private_directory(&directory)?;
    Ok(directory)
}

/// Open a directory for DACL/label changes relative to an already checked parent.
#[allow(dead_code)] // Used by the AppContainer candidate once it is integrated.
pub(crate) fn open_security_directory(dir: &Dir, name: &str) -> io::Result<File> {
    open_directory_handle(
        dir,
        name,
        FILE_READ_ATTRIBUTES | READ_CONTROL | WRITE_DAC | WRITE_OWNER | SYNCHRONIZE,
        FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE,
    )
}

/// Hold a directory identity stable against rename and removal during an operation.
pub(crate) fn open_pinned_directory(dir: &Dir, name: &str) -> io::Result<File> {
    open_directory_handle(
        dir,
        name,
        FILE_READ_ATTRIBUTES | SYNCHRONIZE,
        FILE_SHARE_READ | FILE_SHARE_WRITE,
    )
}

/// Remove a current-user private scratch tree. No pathname is reopened after
/// its file identity is checked, and child lookup is relative to the kept handle.
pub(crate) fn remove_private_tree(
    parent: &Dir,
    name: &str,
    expected: &FileIdentity,
    max_entries: usize,
    max_depth: usize,
) -> io::Result<()> {
    let root = open_cleanup_entry(parent, &name.encode_utf16().collect::<Vec<_>>(), true)?;
    verify_cleanup_entry(&root, expected, true)?;
    verify_private_directory(&root)?;
    let mut remaining = max_entries;
    remove_cleanup_directory(&root, expected.volume, &mut remaining, max_depth)
}

struct CleanupEntry {
    name: Vec<u16>,
    identity: FileIdentity,
    directory: bool,
}

fn open_cleanup_entry(
    parent: &impl AsRawHandle,
    name: &[u16],
    directory: bool,
) -> io::Result<File> {
    let extra_access = if directory {
        FILE_LIST_DIRECTORY | READ_CONTROL
    } else {
        FILE_WRITE_ATTRIBUTES
    };
    open_entry_for_move(parent, name, directory, extra_access)
}

pub(crate) fn open_verified_entry(
    parent: &Dir,
    name: &str,
    directory: bool,
    unlink: bool,
) -> io::Result<File> {
    let mut extra_access = 0;
    if !directory {
        extra_access = FILE_READ_DATA;
        if unlink {
            extra_access |= FILE_WRITE_ATTRIBUTES;
        }
    }
    open_entry_for_move(
        parent,
        &name.encode_utf16().collect::<Vec<_>>(),
        directory,
        extra_access,
    )
}

fn open_entry_for_move(
    parent: &impl AsRawHandle,
    name: &[u16],
    directory: bool,
    extra_access: u32,
) -> io::Result<File> {
    if name.is_empty()
        || name == [b'.' as u16]
        || name == [b'.' as u16, b'.' as u16]
        || "\0/:\\".encode_utf16().any(|unit| name.contains(&unit))
    {
        return Err(io::Error::other("invalid scratch entry name"));
    }
    let length = u16::try_from(std::mem::size_of_val(name))
        .map_err(|_| io::Error::other("scratch entry name too long"))?;
    let filename = UNICODE_STRING {
        Length: length,
        MaximumLength: length,
        Buffer: name.as_ptr().cast_mut(),
    };
    let attributes = OBJECT_ATTRIBUTES {
        Length: mem::size_of::<OBJECT_ATTRIBUTES>() as u32,
        RootDirectory: parent.as_raw_handle(),
        ObjectName: &filename,
        Attributes: OBJ_DONT_REPARSE,
        SecurityDescriptor: ptr::null_mut(),
        SecurityQualityOfService: ptr::null_mut(),
    };
    let kind = if directory {
        FILE_DIRECTORY_FILE
    } else {
        FILE_NON_DIRECTORY_FILE
    };
    let mut raw = ptr::null_mut();
    let mut status_block = IO_STATUS_BLOCK::default();
    let status = unsafe {
        NtOpenFile(
            &mut raw,
            DELETE | FILE_READ_ATTRIBUTES | SYNCHRONIZE | extra_access,
            &attributes,
            &mut status_block,
            FILE_SHARE_READ,
            kind | FILE_SYNCHRONOUS_IO_NONALERT | FILE_OPEN_REPARSE_POINT,
        )
    };
    if status != STATUS_SUCCESS {
        return Err(io::Error::from_raw_os_error(
            unsafe { RtlNtStatusToDosError(status) } as i32,
        ));
    }
    Ok(unsafe { File::from_raw_handle(raw) })
}

pub(crate) fn verify_cleanup_entry(
    file: &File,
    expected: &FileIdentity,
    directory: bool,
) -> io::Result<()> {
    use std::os::windows::fs::MetadataExt;
    let metadata = file.metadata()?;
    if metadata.file_attributes() & (FILE_ATTRIBUTE_REPARSE_POINT | FILE_ATTRIBUTE_DEVICE) != 0
        || (directory && !metadata.is_dir())
        || (!directory && !metadata.is_file())
        || &identity_handle(file)? != expected
    {
        return Err(io::Error::other("scratch entry identity or type changed"));
    }
    Ok(())
}

fn cleanup_entries(
    directory: &File,
    volume: u64,
    remaining: &mut usize,
) -> io::Result<Vec<CleanupEntry>> {
    let mut entries = Vec::new();
    // The file-system records use 8-byte alignment and a variable UTF-16 name.
    let mut storage = [0u64; 512];
    let mut restart = true;
    loop {
        let mut status_block = IO_STATUS_BLOCK::default();
        let status = unsafe {
            NtQueryDirectoryFile(
                directory.as_raw_handle(),
                ptr::null_mut(),
                None,
                ptr::null(),
                &mut status_block,
                storage.as_mut_ptr().cast(),
                mem::size_of_val(&storage) as u32,
                FileIdExtdDirectoryInformation,
                true,
                ptr::null(),
                restart,
            )
        };
        if status == STATUS_NO_MORE_FILES || (restart && status == STATUS_NO_SUCH_FILE) {
            return Ok(entries);
        }
        if status != STATUS_SUCCESS {
            return Err(io::Error::from_raw_os_error(
                unsafe { RtlNtStatusToDosError(status) } as i32,
            ));
        }
        restart = false;
        let start = mem::offset_of!(FILE_ID_EXTD_DIR_INFORMATION, FileName);
        let info = unsafe { &*storage.as_ptr().cast::<FILE_ID_EXTD_DIR_INFORMATION>() };
        let length = info.FileNameLength as usize;
        if info.NextEntryOffset != 0
            || length == 0
            || length % 2 != 0
            || status_block.Information > mem::size_of_val(&storage)
            || start + length > status_block.Information
        {
            return Err(io::Error::other("invalid scratch directory record"));
        }
        let name = unsafe {
            std::slice::from_raw_parts(storage.as_ptr().cast::<u8>().add(start).cast(), length / 2)
        };
        if name == [b'.' as u16] || name == [b'.' as u16, b'.' as u16] {
            continue;
        }
        if *remaining == 0 {
            return Err(io::Error::other("scratch cleanup entry limit exceeded"));
        }
        *remaining -= 1;
        if info.FileAttributes & (FILE_ATTRIBUTE_REPARSE_POINT | FILE_ATTRIBUTE_DEVICE) != 0 {
            return Err(io::Error::other("scratch cleanup found a special entry"));
        }
        entries.push(CleanupEntry {
            name: name.to_vec(),
            identity: FileIdentity {
                volume,
                index: info.FileId.Identifier,
            },
            directory: info.FileAttributes & FILE_ATTRIBUTE_DIRECTORY != 0,
        });
    }
}

fn remove_cleanup_directory(
    directory: &File,
    volume: u64,
    remaining: &mut usize,
    depth: usize,
) -> io::Result<()> {
    for entry in cleanup_entries(directory, volume, remaining)? {
        if entry.directory && depth == 0 {
            return Err(io::Error::other("scratch cleanup depth limit exceeded"));
        }
        let file = open_cleanup_entry(directory, &entry.name, entry.directory)?;
        verify_cleanup_entry(&file, &entry.identity, entry.directory)?;
        if entry.directory {
            remove_cleanup_directory(&file, volume, remaining, depth - 1)?;
        } else {
            unlink_cleanup_entry(&file, FILE_DISPOSITION_FLAG_IGNORE_READONLY_ATTRIBUTE)?;
        }
    }
    unlink_cleanup_entry(directory, 0)
}

fn unlink_cleanup_entry(file: &File, flags: u32) -> io::Result<()> {
    // The disposition removes the link opened by this handle on close. Ignoring
    // the read-only attribute leaves it untouched on remaining hard links.
    let flags = FILE_DISPOSITION_FLAG_DELETE | FILE_DISPOSITION_FLAG_POSIX_SEMANTICS | flags;
    let mut status_block = IO_STATUS_BLOCK::default();
    let status = unsafe {
        NtSetInformationFile(
            file.as_raw_handle(),
            &mut status_block,
            (&flags as *const u32).cast(),
            mem::size_of_val(&flags) as u32,
            FileDispositionInformationEx,
        )
    };
    if status != STATUS_SUCCESS {
        return Err(io::Error::from_raw_os_error(
            unsafe { RtlNtStatusToDosError(status) } as i32,
        ));
    }
    Ok(())
}

pub(crate) fn unlink_verified_entry(file: &File, directory: bool) -> io::Result<()> {
    let flags = if directory {
        0
    } else {
        FILE_DISPOSITION_FLAG_IGNORE_READONLY_ATTRIBUTE
    };
    unlink_cleanup_entry(file, flags)
}

pub(crate) fn open_pinned_private_file(dir: &Dir, name: &str) -> io::Result<File> {
    let file = open_file_handle(dir, name, FILE_SHARE_READ | FILE_SHARE_WRITE)?;
    verify_private_inherited_file(&file)?;
    Ok(file)
}

pub(crate) fn open_unchanging_file(dir: &Dir, name: &str) -> io::Result<File> {
    let file = open_file_handle(dir, name, FILE_SHARE_READ)?;
    if !has_single_link(&file)? {
        return Err(io::Error::new(
            io::ErrorKind::PermissionDenied,
            "hard-linked file rejected",
        ));
    }
    Ok(file)
}

fn open_file_handle(dir: &Dir, name: &str, share: u32) -> io::Result<File> {
    let wide: Vec<u16> = name.encode_utf16().collect();
    let length = wide
        .len()
        .checked_mul(2)
        .and_then(|n| u16::try_from(n).ok())
        .ok_or_else(|| io::Error::other("file name too long"))?;
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
        SecurityDescriptor: ptr::null_mut(),
        SecurityQualityOfService: ptr::null(),
    };
    let mut handle = ptr::null_mut();
    let mut status_block = IO_STATUS_BLOCK::default();
    let status = unsafe {
        NtCreateFile(
            &mut handle,
            GENERIC_READ | READ_CONTROL | SYNCHRONIZE,
            &attributes,
            &mut status_block,
            ptr::null(),
            FILE_ATTRIBUTE_NORMAL,
            share,
            FILE_OPEN,
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
    Ok(file)
}

fn open_directory_handle(dir: &Dir, name: &str, access: u32, share: u32) -> io::Result<File> {
    let wide: Vec<u16> = name.encode_utf16().collect();
    let length = wide
        .len()
        .checked_mul(2)
        .and_then(|n| u16::try_from(n).ok())
        .ok_or_else(|| io::Error::other("directory name too long"))?;
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
        SecurityDescriptor: ptr::null_mut(),
        SecurityQualityOfService: ptr::null(),
    };
    let mut handle = ptr::null_mut();
    let mut status_block = IO_STATUS_BLOCK::default();
    let status = unsafe {
        NtCreateFile(
            &mut handle,
            access,
            &attributes,
            &mut status_block,
            ptr::null(),
            FILE_ATTRIBUTE_DIRECTORY,
            share,
            FILE_OPEN,
            FILE_DIRECTORY_FILE | FILE_SYNCHRONOUS_IO_NONALERT | FILE_OPEN_REPARSE_POINT,
            ptr::null(),
            0,
        )
    };
    if status < 0 {
        return Err(io::Error::from_raw_os_error(
            unsafe { RtlNtStatusToDosError(status) } as i32,
        ));
    }
    let directory = unsafe { File::from_raw_handle(handle) };
    use std::os::windows::fs::MetadataExt;
    if directory.metadata()?.file_attributes() & FILE_ATTRIBUTE_REPARSE_POINT != 0 {
        return Err(io::Error::new(
            io::ErrorKind::PermissionDenied,
            "reparse directory rejected",
        ));
    }
    Ok(directory)
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
    verify_private_acl(file, 0, true)
}

pub(crate) fn verify_private_directory(file: &impl AsRawHandle) -> io::Result<()> {
    // Explicit object/container inheritance, no inherited or unrelated grants.
    verify_private_acl(file, 3, true)
}

/// Conservatively reject a directory writable by principals outside this user,
/// SYSTEM and local administrators. This inspects but never changes its DACL.
pub(crate) fn verify_trusted_directory(file: &impl AsRawHandle) -> io::Result<()> {
    let user = UserSid::current()?;
    let mut system = [0usize; 16];
    let mut administrators = [0usize; 16];
    let mut system_size = mem::size_of_val(&system) as u32;
    let mut administrators_size = mem::size_of_val(&administrators) as u32;
    let system_sid = system.as_mut_ptr().cast();
    let administrators_sid = administrators.as_mut_ptr().cast();
    if unsafe {
        CreateWellKnownSid(
            WinLocalSystemSid,
            ptr::null_mut(),
            system_sid,
            &mut system_size,
        )
    } == 0
        || unsafe {
            CreateWellKnownSid(
                WinBuiltinAdministratorsSid,
                ptr::null_mut(),
                administrators_sid,
                &mut administrators_size,
            )
        } == 0
    {
        return Err(io::Error::last_os_error());
    }
    let trusted = |sid: PSID| unsafe {
        EqualSid(sid, user.sid()) != 0
            || EqualSid(sid, system_sid) != 0
            || EqualSid(sid, administrators_sid) != 0
    };
    let invalid = || {
        io::Error::new(
            io::ErrorKind::PermissionDenied,
            "directory ACL permits untrusted modification or cannot be verified",
        )
    };
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
    let _allocation = LocalAllocation(descriptor);
    let mut control = 0;
    let mut revision = 0;
    if owner.is_null()
        || acl.is_null()
        || descriptor.is_null()
        || unsafe { IsValidSid(owner) } == 0
        || !trusted(owner)
        || unsafe { GetSecurityDescriptorControl(descriptor, &mut control, &mut revision) } == 0
        || control & SE_DACL_PRESENT == 0
    {
        return Err(invalid());
    }
    const INHERIT_ONLY: u8 = 0x08;
    const DANGEROUS: u32 = FILE_WRITE_DATA
        | FILE_APPEND_DATA
        | FILE_WRITE_EA
        | FILE_WRITE_ATTRIBUTES
        | FILE_DELETE_CHILD
        | DELETE
        | WRITE_DAC
        | WRITE_OWNER
        | 0x1000_0000
        | 0x4000_0000
        | 0x0200_0000;
    let acl_size = unsafe { (*acl).AclSize as usize };
    for index in 0..unsafe { (*acl).AceCount } as u32 {
        let mut raw = ptr::null_mut();
        if unsafe { GetAce(acl, index, &mut raw) } == 0 || raw.is_null() {
            return Err(invalid());
        }
        let offset = (raw as usize)
            .checked_sub(acl as usize)
            .ok_or_else(invalid)?;
        if offset
            .checked_add(mem::size_of::<ACE_HEADER>())
            .is_none_or(|end| end > acl_size)
        {
            return Err(invalid());
        }
        let header = unsafe { &*raw.cast::<ACE_HEADER>() };
        if !matches!(header.AceType, 0 | 1)
            || header.AceFlags & !0x1f != 0
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
        let mask = unsafe { raw.cast::<u8>().add(4).cast::<u32>().read_unaligned() };
        if header.AceType == 0
            && header.AceFlags & INHERIT_ONLY == 0
            && mask & DANGEROUS != 0
            && !trusted(sid)
        {
            return Err(invalid());
        }
    }
    Ok(())
}

/// Check an ordinary file inheriting only the verified private directory grants.
pub(crate) fn verify_private_inherited_file(file: &impl AsRawHandle) -> io::Result<()> {
    let mut info: BY_HANDLE_FILE_INFORMATION = unsafe { mem::zeroed() };
    if unsafe { GetFileInformationByHandle(file.as_raw_handle(), &mut info) } == 0 {
        return Err(io::Error::last_os_error());
    }
    if info.nNumberOfLinks != 1
        || info.dwFileAttributes & (FILE_ATTRIBUTE_REPARSE_POINT | FILE_ATTRIBUTE_DIRECTORY) != 0
    {
        return Err(io::Error::new(
            io::ErrorKind::PermissionDenied,
            "private file identity invalid",
        ));
    }
    verify_private_acl(file, 0x10, false)
}

pub(crate) fn has_single_link(file: &impl AsRawHandle) -> io::Result<bool> {
    let mut info: BY_HANDLE_FILE_INFORMATION = unsafe { mem::zeroed() };
    if unsafe { GetFileInformationByHandle(file.as_raw_handle(), &mut info) } == 0 {
        return Err(io::Error::last_os_error());
    }
    Ok(info.nNumberOfLinks == 1
        && info.dwFileAttributes & (FILE_ATTRIBUTE_REPARSE_POINT | FILE_ATTRIBUTE_DIRECTORY) == 0)
}

fn verify_private_acl(
    file: &impl AsRawHandle,
    expected_flags: u8,
    protected: bool,
) -> io::Result<()> {
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
        || (protected && control & SE_DACL_PROTECTED == 0)
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
            || header.AceFlags != expected_flags
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
    rename_open_entry(&file, to, target, replace)
}

pub(crate) fn rename_verified_entry(file: &File, to: &Dir, target: &str) -> io::Result<()> {
    rename_open_entry(file, to, target, false)
}

fn rename_open_entry(
    file: &impl AsRawHandle,
    to: &Dir,
    target: &str,
    replace: bool,
) -> io::Result<()> {
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
