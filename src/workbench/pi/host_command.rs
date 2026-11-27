use super::readiness::host_command_environment;
use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
    fs::{Root, platform},
    workbench::process,
};
use std::{
    os::{fd::AsRawFd, unix::process::CommandExt},
    path::Path,
    process::{Command, ExitStatus},
    time::Duration,
};

pub(super) fn run(
    cwd: &Path,
    dir: cap_std::fs::Dir,
    script: &str,
    cancel: &Cancellation,
    publish: impl FnMut(Option<(bool, &[u8])>) -> Result<()>,
) -> Result<ExitStatus> {
    cancel.check()?;
    let verified = Root::open(cwd)?;
    if platform::identity_dir(&dir)? != verified.identity {
        return Err(YssError::new(
            ErrorCode::ConcurrentChange,
            "本机命令的工作目录已改变",
        ));
    }
    let mut command = Command::new("/bin/bash");
    command
        .args(["--noprofile", "--norc", "-c", script])
        .env_clear()
        .envs(host_command_environment()?)
        .env("NO_COLOR", "1");
    // Enter the verified directory descriptor before exec. Only fchdir runs after the fork.
    unsafe {
        command.pre_exec(move || {
            if libc::fchdir(dir.as_raw_fd()) == -1 {
                return Err(std::io::Error::last_os_error());
            }
            Ok(())
        });
    }
    verified.revalidate()?;
    process::stream_command_output(command, cancel, Duration::from_secs(600), publish)
}
