use semver::Version;
use serde_json::Value;
use std::{env, io::Read, path::PathBuf};

pub(super) fn corepack_package(version: &Version) -> Option<PathBuf> {
    let home = env::var_os("COREPACK_HOME").map(PathBuf::from).or_else(|| {
        let base = env::var_os("XDG_CACHE_HOME")
            .or_else(|| env::var_os("LOCALAPPDATA"))
            .map(PathBuf::from)
            .or_else(|| {
                if cfg!(windows) {
                    env::var_os("USERPROFILE").map(|home| PathBuf::from(home).join("AppData/Local"))
                } else {
                    env::var_os("HOME").map(|home| PathBuf::from(home).join(".cache"))
                }
            })?;
        Some(base.join("node/corepack"))
    });
    let home = home.filter(|path| path.is_absolute())?;
    Some(home.join("v1/pnpm").join(version.to_string()))
}

pub(super) fn corepack_hash_matches(version: &Version, hash: &str) -> bool {
    let Some(package) = corepack_package(version) else {
        return false;
    };
    // Corepack can reuse an entry without comparing the requested locator hash.
    let receipt = package.join(".corepack");
    if !receipt
        .symlink_metadata()
        .is_ok_and(|metadata| metadata.is_file())
    {
        return false;
    }
    std::fs::File::open(receipt)
        .ok()
        .and_then(|file| serde_json::from_reader::<_, Value>(file.take(16 * 1024)).ok())
        .is_some_and(|value| value["hash"].as_str() == Some(hash))
}

pub(super) fn integrity_hash(pin: &Version) -> Option<&str> {
    let hash = pin.build.as_str();
    let (algorithm, _) = hash.split_once('.')?;
    matches!(
        algorithm,
        "sha1" | "sha224" | "sha256" | "sha384" | "sha512"
    )
    .then_some(hash)
}
