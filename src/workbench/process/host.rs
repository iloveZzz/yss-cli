//! Recover the current user's interactive login environment for a Finder-launched sidecar.
//!
//! Only selected configuration is passed on. The shell output is never logged and the
//! existing Pi executable, configuration-directory and sandbox checks still apply.

use crate::{cancel::Cancellation, workbench::process};
use std::{
    env,
    ffi::{CStr, OsStr, OsString},
    io::Read,
    mem::MaybeUninit,
    os::{
        fd::AsRawFd,
        unix::{
            ffi::{OsStrExt, OsStringExt},
            net::UnixStream,
            process::CommandExt,
        },
    },
    path::{Path, PathBuf},
    process::Command,
    ptr, thread,
    time::Duration,
};

fn login_shell() -> Option<PathBuf> {
    let mut account = MaybeUninit::<libc::passwd>::uninit();
    let mut result = ptr::null_mut();
    let size = unsafe { libc::sysconf(libc::_SC_GETPW_R_SIZE_MAX) };
    let mut buffer = vec![
        0;
        usize::try_from(size)
            .unwrap_or(0)
            .clamp(16 * 1024, 1024 * 1024)
    ];
    let code = unsafe {
        libc::getpwuid_r(
            libc::geteuid(),
            account.as_mut_ptr(),
            buffer.as_mut_ptr().cast(),
            buffer.len(),
            &mut result,
        )
    };
    if code != 0 || result.is_null() {
        return None;
    }
    let account = unsafe { account.assume_init() };
    if account.pw_shell.is_null() {
        return None;
    }
    let path = PathBuf::from(OsStr::from_bytes(unsafe {
        CStr::from_ptr(account.pw_shell).to_bytes()
    }));
    (path.is_absolute() && path.is_file()).then_some(path)
}

fn supported_variable(name: &str) -> bool {
    matches!(
        name,
        "YSS_PI_EXECUTABLE"
            | "PI_CODING_AGENT_DIR"
            | "YSS_PI_MAVEN_REPOSITORY"
            | "YSS_PI_PNPM_STORE"
            | "JAVA_HOME"
            | "GH_CONFIG_DIR"
            | "GLAB_CONFIG_DIR"
            | "GH_HOST"
            | "GITLAB_HOST"
            | "GH_TOKEN"
            | "GH_ENTERPRISE_TOKEN"
            | "GITLAB_TOKEN"
            | "GLAB_TOKEN"
            | "AWS_ACCESS_KEY_ID"
            | "AWS_SECRET_ACCESS_KEY"
            | "AWS_SESSION_TOKEN"
            | "AWS_PROFILE"
            | "AWS_REGION"
            | "AWS_DEFAULT_REGION"
            | "GOOGLE_CLOUD_PROJECT"
            | "GOOGLE_CLOUD_LOCATION"
            | "AZURE_OPENAI_ENDPOINT"
    ) || name.ends_with("_API_KEY")
        || name.ends_with("_AUTH_TOKEN")
}

/// Add a bounded, selected login-shell environment to the sidecar command.
///
/// Returns false if discovery fails; in that case the command is left unchanged.
/// Values already inherited by the desktop process take precedence, except that
/// the login shell's PATH is used to match the user's normal command lookup.
pub fn apply_macos_login_environment(command: &mut Command) -> bool {
    let Some(shell) = login_shell() else {
        return false;
    };
    let Ok(marker) = crate::fs::unique_id() else {
        return false;
    };
    // `marker` is generated hexadecimal, not derived from shell input.
    // Values (including credentials) travel only through an inherited anonymous
    // socket, never through command arguments, stdout, stderr, or log output.
    let script = format!(
        "command printf '\\0%s\\0' '{marker}' >&9; /usr/bin/env -0 >&9; command printf '\\0%s\\0' '{marker}' >&9"
    );
    let Ok((reader, writer)) = UnixStream::pair() else {
        return false;
    };
    let mut shell_command = Command::new(shell);
    shell_command.args(["-i", "-l", "-c", &script]);
    // SAFETY: only async-signal-safe descriptor operations occur after fork.
    unsafe {
        shell_command.pre_exec(move || {
            let source = writer.as_raw_fd();
            if libc::dup2(source, 9) == -1 || libc::fcntl(9, libc::F_SETFD, 0) == -1 {
                return Err(std::io::Error::last_os_error());
            }
            Ok(())
        });
    }
    let output_reader = thread::spawn(move || {
        let mut output = Vec::new();
        reader
            .take(2 * 1024 * 1024 + 1)
            .read_to_end(&mut output)
            .ok()?;
        (output.len() <= 2 * 1024 * 1024).then_some(output)
    });
    let result =
        process::capture_command(shell_command, &Cancellation::new(), Duration::from_secs(5));
    let Ok(Some(output)) = output_reader.join() else {
        return false;
    };
    let Ok((status, _, _)) = result else {
        return false;
    };
    if !status.success() {
        return false;
    }
    let frame = [b"\0".as_slice(), marker.as_bytes(), b"\0".as_slice()].concat();
    let Some(start) = output.windows(frame.len()).position(|part| part == frame) else {
        return false;
    };
    let start = start + frame.len();
    let Some(end) = output[start..]
        .windows(frame.len())
        .position(|part| part == frame)
    else {
        return false;
    };
    let entries = output[start..start + end].split(|byte| *byte == 0);
    let mut values = Vec::new();
    let mut path = None;
    for entry in entries.filter(|entry| !entry.is_empty()) {
        let Some(separator) = entry.iter().position(|byte| *byte == b'=') else {
            return false;
        };
        let Ok(name) = std::str::from_utf8(&entry[..separator]) else {
            return false;
        };
        let value = OsString::from_vec(entry[separator + 1..].to_vec());
        if name == "PATH" {
            let absolute: Vec<_> = env::split_paths(&value)
                .filter(|item| item.is_absolute() && Path::new(item).is_dir())
                .collect();
            if !absolute.is_empty() {
                path = env::join_paths(absolute).ok();
            }
        } else if supported_variable(name) && env::var_os(name).is_none() {
            values.push((name.to_owned(), value));
        }
    }
    let Some(path) = path else {
        return false;
    };
    command.env("PATH", path).envs(values);
    true
}
