use super::private_dir;
use crate::{
    error::Result,
    fs::{Root, unique_id},
    model::{MAX_MANIFEST, RelPath},
    workbench::pnpm::{corepack_hash_matches, corepack_package, integrity_hash},
};
use semver::{BuildMetadata, Version};
use serde_json::Value;
use std::{
    fs::{self, OpenOptions},
    io::Write,
    os::unix::fs::OpenOptionsExt,
    path::{Path, PathBuf},
};

pub(super) struct PinnedLauncher {
    pub(super) bin: PathBuf,
    pub(super) package: Option<PathBuf>,
}

impl Drop for PinnedLauncher {
    fn drop(&mut self) {
        let _ = fs::remove_dir_all(&self.bin);
    }
}

fn read_manifest(root: &Root, path: &Path) -> Option<Value> {
    let rel = RelPath::new(path.to_str()?).ok()?;
    let file = root.read_checked(&rel, MAX_MANIFEST).ok()?;
    serde_json::from_slice(&file.bytes).ok()
}

fn project_pin(root: &Path, cwd: &Path) -> Option<Version> {
    let project = Root::open(root).ok()?;
    let mut path = cwd.strip_prefix(root).ok()?;
    loop {
        if let Some(pin) = read_manifest(&project, &path.join("package.json"))
            .and_then(|manifest| manifest["packageManager"].as_str().map(str::to_owned))
        {
            return Version::parse(pin.strip_prefix("pnpm@")?).ok();
        }
        path = path.parent()?;
    }
}

fn shell_path(path: &Path) -> Option<String> {
    Some(format!("'{}'", path.to_str()?.replace('\'', "'\\''")))
}

fn reject(code: &str) -> String {
    format!("#!/bin/bash\nprintf '%s\\n' 'yss-pi-pnpm: {code}' >&2\nexit 1\n")
}

impl PinnedLauncher {
    pub(super) fn prepare(
        root: &Path,
        cwd: &Path,
        scratch_parent: &Path,
        node: Option<&Path>,
        tool_allowed: impl Fn(&Path) -> bool,
    ) -> Result<Option<Self>> {
        let Some(pin) = project_pin(root, cwd) else {
            return Ok(None);
        };
        let hash = integrity_hash(&pin).map(str::to_owned);
        let mut version = pin;
        if hash.is_some() {
            version.build = BuildMetadata::EMPTY;
        }
        let package = corepack_package(&version).and_then(|path| {
            let parent = path.parent()?.canonicalize().ok()?;
            let package = path.canonicalize().ok()?;
            (package.parent() == Some(parent.as_path())
                && package.file_name() == path.file_name()
                && tool_allowed(&package))
            .then_some(package)
        });
        let (package, script) = if hash
            .as_deref()
            .is_some_and(|hash| package.is_none() || !corepack_hash_matches(&version, hash))
        {
            (None, reject("PNPM_INTEGRITY_MISMATCH"))
        } else if let Some(script) = package
            .as_deref()
            .and_then(|package| cached_entry(package, &version, node))
        {
            (package, script)
        } else if hash.is_some() {
            (None, reject("PNPM_PIN_UNAVAILABLE"))
        } else {
            return Ok(None);
        };
        let bin = scratch_parent.join(format!("pnpm-{}", unique_id()?));
        private_dir(&bin)?;
        let launcher = Self { bin, package };
        OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o500)
            .open(launcher.bin.join("pnpm"))?
            .write_all(script.as_bytes())?;
        Ok(Some(launcher))
    }
}

fn cached_entry(package: &Path, version: &Version, node: Option<&Path>) -> Option<String> {
    let node = shell_path(node?)?;
    let manifest = read_manifest(&Root::open(package).ok()?, Path::new("package.json"))?;
    if manifest["name"] != "pnpm" || Version::parse(manifest["version"].as_str()?).ok()? != *version
    {
        return None;
    }
    let entry = ["bin/pnpm.cjs", "bin/pnpm.mjs"]
        .into_iter()
        .filter_map(|entry| package.join(entry).canonicalize().ok())
        .find(|entry| entry.starts_with(package) && entry.is_file())?;
    let entry = shell_path(&entry)?;
    // Keep the selected version fixed when a script invokes pnpm again.
    // pnpm 10 and pnpm 11+ read different configuration variables.
    Some(format!(
        "#!/bin/bash\nexport npm_config_manage_package_manager_versions=false npm_config_package_manager_strict_version=true pnpm_config_pm_on_fail=error\nexec {node} {entry} \"$@\"\n"
    ))
}
