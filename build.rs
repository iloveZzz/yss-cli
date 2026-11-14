use sha2::{Digest, Sha256};
use std::{env, fs, path::Path, process::Command};

fn git(args: &[&str]) -> Option<Vec<u8>> {
    let mut command = Command::new("git");
    for (key, _) in env::vars_os() {
        if key.to_string_lossy().starts_with("GIT_") {
            command.env_remove(key);
        }
    }
    let out = command.args(args).output().ok()?;
    out.status.success().then_some(out.stdout)
}

fn main() {
    println!("cargo:rerun-if-changed=src");
    println!("cargo:rerun-if-changed=build.rs");
    println!("cargo:rerun-if-changed=Cargo.toml");
    println!("cargo:rerun-if-changed=Cargo.lock");
    println!("cargo:rerun-if-changed=rust-toolchain.toml");
    println!("cargo:rerun-if-changed=assets/bundle.tar.gz");
    if let Some(dir) = git(&["rev-parse", "--git-dir"])
        && let Ok(dir) = String::from_utf8(dir)
    {
        println!("cargo:rerun-if-changed={}/HEAD", dir.trim());
        println!("cargo:rerun-if-changed={}/index", dir.trim());
        if let Some(reference) = git(&["rev-parse", "--git-path", "HEAD"])
            && let Ok(reference) = String::from_utf8(reference)
        {
            println!("cargo:rerun-if-changed={}", reference.trim());
        }
    }
    if let Some(reference) =
        git(&["symbolic-ref", "-q", "HEAD"]).and_then(|v| String::from_utf8(v).ok())
        && let Some(path) = git(&["rev-parse", "--git-path", reference.trim()])
            .and_then(|v| String::from_utf8(v).ok())
    {
        println!("cargo:rerun-if-changed={}", path.trim());
    }
    if let Some(path) =
        git(&["rev-parse", "--git-path", "packed-refs"]).and_then(|v| String::from_utf8(v).ok())
    {
        println!("cargo:rerun-if-changed={}", path.trim());
    }
    let commit = git(&["rev-parse", "HEAD"])
        .and_then(|v| String::from_utf8(v).ok())
        .unwrap_or_else(|| "unknown".into());
    let state = match git(&["status", "--porcelain", "--untracked-files=all"]) {
        Some(bytes) if bytes.is_empty() => "committed",
        Some(_) => "working-tree",
        None => "unknown",
    };
    let mut paths = vec![
        "Cargo.toml".to_owned(),
        "Cargo.lock".to_owned(),
        "rust-toolchain.toml".to_owned(),
        "build.rs".to_owned(),
    ];
    fn walk(dir: &Path, paths: &mut Vec<String>, rust_only: bool) {
        for entry in fs::read_dir(dir).expect("cannot list build sources") {
            let entry = entry.expect("cannot read source entry");
            let path = entry.path();
            let kind = entry.file_type().expect("cannot inspect source entry");
            assert!(!kind.is_symlink(), "build sources cannot contain links");
            if kind.is_dir() {
                walk(&path, paths, rust_only);
            } else if kind.is_file() && (!rust_only || path.extension().is_some_and(|v| v == "rs"))
            {
                paths.push(
                    path.to_str()
                        .expect("source path is not UTF-8")
                        .replace('\\', "/"),
                );
            }
        }
    }
    walk(Path::new("src"), &mut paths, true);
    for path in [
        "ui/package.json",
        "ui/pnpm-lock.yaml",
        "ui/pnpm-workspace.yaml",
        "ui/index.html",
        "ui/vite.config.ts",
        "ui/tsconfig.json",
        "ui/eslint.config.mjs",
        "ui/components.json",
    ] {
        println!("cargo:rerun-if-changed={path}");
        if Path::new(path).exists() {
            assert!(
                !fs::symlink_metadata(path)
                    .expect("UI source metadata")
                    .file_type()
                    .is_symlink(),
                "UI sources cannot contain links"
            );
            paths.push(path.to_owned());
        }
    }
    for path in ["ui/src", "ui/public", "ui/dist"] {
        println!("cargo:rerun-if-changed={path}");
        if Path::new(path).exists() {
            assert!(
                !fs::symlink_metadata(path)
                    .expect("UI source metadata")
                    .file_type()
                    .is_symlink(),
                "UI sources cannot contain links"
            );
            walk(Path::new(path), &mut paths, false);
        }
    }
    paths.sort();
    let mut hash = Sha256::new();
    for path in paths {
        hash.update((path.len() as u64).to_be_bytes());
        hash.update(path.as_bytes());
        let bytes = fs::read(&path).expect("cannot read build source");
        hash.update((bytes.len() as u64).to_be_bytes());
        hash.update(bytes);
    }
    let source_digest = format!("{:x}", hash.finalize());
    println!("cargo:rustc-env=YSS_SOURCE_DIGEST={source_digest}");
    println!("cargo:rustc-env=YSS_COMMIT={}", commit.trim());
    println!("cargo:rustc-env=YSS_SOURCE_STATE={state}");
    println!("cargo:rustc-env=YSS_TARGET={}", env::var("TARGET").unwrap());
    let bundle_hash = fs::read("assets/bundle.tar.gz")
        .map(|bytes| format!("{:x}", Sha256::digest(bytes)))
        .unwrap_or_default();
    println!("cargo:rustc-env=YSS_BUNDLE_DIGEST={bundle_hash}");
    let info = serde_json::json!({"version":env::var("CARGO_PKG_VERSION").unwrap(),"target":env::var("TARGET").unwrap(),
        "source":{"commit":commit.trim(),"state":state,"snapshot":source_digest},
        "bundleDigest":if bundle_hash.is_empty(){serde_json::Value::Null}else{serde_json::Value::String(bundle_hash)}});
    let json = serde_json::to_vec(&info).expect("build information");
    let mut record = (json.len() as u32).to_le_bytes().to_vec();
    record.extend(json);
    fs::write(
        Path::new(&env::var("OUT_DIR").unwrap()).join("build-record.bin"),
        record,
    )
    .expect("write build information");
}
