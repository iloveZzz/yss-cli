use std::{
    collections::BTreeSet,
    env,
    ffi::{OsStr, OsString, c_void},
    os::windows::{
        ffi::OsStringExt,
        io::{AsRawHandle, FromRawHandle, OwnedHandle},
    },
    process::Command,
    ptr::{self, NonNull},
};
use windows_sys::Win32::{
    Security::{TOKEN_DUPLICATE, TOKEN_QUERY},
    System::{
        Environment::{CreateEnvironmentBlock, DestroyEnvironmentBlock},
        Threading::{GetCurrentProcess, OpenProcessToken},
    },
};

struct UserEnvironment(NonNull<c_void>);

impl Drop for UserEnvironment {
    fn drop(&mut self) {
        // The buffer was allocated by CreateEnvironmentBlock and never transferred.
        unsafe { DestroyEnvironmentBlock(self.0.as_ptr()) };
    }
}

fn current_user_path() -> Option<OsString> {
    let mut token = ptr::null_mut();
    if unsafe {
        OpenProcessToken(
            GetCurrentProcess(),
            TOKEN_QUERY | TOKEN_DUPLICATE,
            &mut token,
        )
    } == 0
        || token.is_null()
    {
        return None;
    }
    // OpenProcessToken returned a new owned handle.
    let token = unsafe { OwnedHandle::from_raw_handle(token) };
    let mut block = ptr::null_mut();
    if unsafe { CreateEnvironmentBlock(&mut block, token.as_raw_handle(), 0) } == 0 {
        return None;
    }
    let block = UserEnvironment(NonNull::new(block)?);
    let mut cursor = block.0.cast::<u16>().as_ptr();
    const PATH_PREFIX: [u16; 5] = [
        b'P' as u16,
        b'A' as u16,
        b'T' as u16,
        b'H' as u16,
        b'=' as u16,
    ];
    // The OS supplies NUL-terminated UTF-16 entries followed by a second NUL.
    // Stop after finding PATH and never copy any other environment value.
    unsafe {
        while cursor.read() != 0 {
            let mut len = 0;
            while cursor.add(len).read() != 0 {
                len += 1;
            }
            let entry = std::slice::from_raw_parts(cursor, len);
            if entry.len() >= PATH_PREFIX.len()
                && entry[..PATH_PREFIX.len()]
                    .iter()
                    .zip(PATH_PREFIX)
                    .all(|(&actual, expected)| {
                        actual == expected
                            || ((b'A' as u16..=b'Z' as u16).contains(&expected)
                                && actual == expected + u16::from(b'a' - b'A'))
                    })
            {
                return Some(OsString::from_wide(&entry[PATH_PREFIX.len()..]));
            }
            cursor = cursor.add(len + 1);
        }
    }
    None
}

fn merged_path(inherited: &OsStr, fresh: &OsStr) -> Option<OsString> {
    let mut seen = BTreeSet::new();
    let mut paths: Vec<_> = env::split_paths(inherited).collect();
    seen.extend(paths.iter().cloned());
    let original_len = paths.len();
    paths.extend(
        env::split_paths(fresh).filter(|path| path.is_absolute() && seen.insert(path.clone())),
    );
    if paths.len() == original_len {
        return None;
    }
    env::join_paths(paths).ok()
}

/// Extend the owned sidecar's PATH with current Windows user/system entries.
/// The desktop may predate a WinGet PATH change. Inherited entries keep their
/// order and only this child process receives the merged value.
pub fn apply_windows_user_path(command: &mut Command) {
    if let Some(fresh) = current_user_path()
        && let Some(path) = merged_path(&env::var_os("PATH").unwrap_or_default(), &fresh)
    {
        command.env("PATH", path);
    }
}
