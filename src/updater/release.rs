use crate::{
    bundle::archive::{self, Entry},
    codec::{self, Limits},
    error::{ErrorCode, Result, YssError},
    fs::{self, FileSnapshot, Root},
    model::{BuildInfo, FileDescriptor, MAX_ASSET, MAX_MANIFEST, RelPath, RenderedTree, Sha256},
};
use object::{Object, ObjectSection};
use serde::{Deserialize, Serialize};
use std::{
    collections::BTreeMap,
    io::{Cursor, Read, Write},
    path::Path,
};
pub const TARGETS: [&str; 6] = [
    "aarch64-apple-darwin",
    "x86_64-apple-darwin",
    "x86_64-unknown-linux-musl",
    "aarch64-unknown-linux-musl",
    "x86_64-pc-windows-msvc",
    "aarch64-pc-windows-msvc",
];
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct ReleaseManifest {
    pub format: String,
    pub format_version: u32,
    pub build: BuildInfo,
    pub payload: BTreeMap<RelPath, FileDescriptor>,
}
pub type ReleaseInfo = BuildInfo;
pub struct VerifiedRelease {
    manifest: ReleaseManifest,
    tree: RenderedTree,
}
impl VerifiedRelease {
    pub fn manifest(&self) -> &ReleaseManifest {
        &self.manifest
    }
    pub fn into_payload_tree(self) -> RenderedTree {
        self.tree
    }
}
#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct PackageReport {
    pub path: String,
    pub sha256: Sha256,
    pub bytes: u64,
    pub manifest: ReleaseManifest,
}
fn invalid(message: &str) -> YssError {
    YssError::new(ErrorCode::InvalidInput, message)
}
pub fn version(value: &str, stable: bool) -> Result<semver::Version> {
    let value = value.strip_prefix('v').unwrap_or(value);
    let version = semver::Version::parse(value)
        .map_err(|_| YssError::new(ErrorCode::Argument, "版本必须为精确 SemVer"))?;
    if value.len() > 128 || !version.build.is_empty() || (stable && !version.pre.is_empty()) {
        return Err(YssError::new(
            ErrorCode::Argument,
            "在线目标必须为正式版本；不支持构建元数据",
        ));
    }
    Ok(version)
}
pub fn filename(version: &str, target: &str) -> Result<String> {
    validate_target(target)?;
    let version = version.strip_prefix('v').unwrap_or(version);
    self::version(version, false)?;
    Ok(format!(
        "yss_{version}_{target}.{}",
        if target.contains("windows") {
            "zip"
        } else {
            "tar.gz"
        }
    ))
}
pub fn binary_name(target: &str) -> Result<&'static str> {
    validate_target(target)?;
    Ok(if target.contains("windows") {
        "yss.exe"
    } else {
        "yss"
    })
}
fn validate_target(target: &str) -> Result<()> {
    if TARGETS.contains(&target) {
        Ok(())
    } else {
        Err(YssError::new(
            ErrorCode::UnsupportedPlatform,
            "不支持此 target",
        ))
    }
}
pub fn binary_info(bytes: &[u8]) -> Result<BuildInfo> {
    let file = object::File::parse(bytes).map_err(|_| invalid("载荷不是可识别的本机程序"))?;
    if !matches!(
        file.kind(),
        object::ObjectKind::Executable | object::ObjectKind::Dynamic
    ) || !file.is_little_endian()
    {
        return Err(invalid("载荷不是受支持的可执行程序"));
    }
    let section = match file.format() {
        object::BinaryFormat::Elf => ".yssbuild",
        object::BinaryFormat::MachO => "__yssbuild",
        object::BinaryFormat::Pe => ".yssmeta",
        _ => return Err(invalid("程序格式不支持")),
    };
    let sections: Vec<_> = file
        .sections()
        .filter(|s| s.name().ok() == Some(section))
        .collect();
    if sections.len() != 1 {
        return Err(YssError::new(
            ErrorCode::UnsupportedFormat,
            "程序缺少唯一的 Rust 构建记录；不支持旧 Go 程序",
        ));
    }
    let data = sections[0]
        .data()
        .map_err(|_| invalid("不能读取构建记录"))?;
    if data.len() < 4 {
        return Err(invalid("构建记录不完整"));
    }
    let size = u32::from_le_bytes(
        data[..4]
            .try_into()
            .map_err(|_| invalid("构建记录长度错误"))?,
    ) as usize;
    if size > MAX_MANIFEST as usize
        || size > data.len() - 4
        || data[size + 4..].iter().any(|b| *b != 0)
    {
        return Err(invalid("构建记录长度错误"));
    }
    let info: BuildInfo = codec::decode_json(&data[4..size + 4], Limits::manifest())?;
    validate_target(&info.target)?;
    version(&info.version, false)?;
    let architecture = if info.target.starts_with("aarch64") {
        object::Architecture::Aarch64
    } else {
        object::Architecture::X86_64
    };
    let format = if info.target.contains("apple") {
        object::BinaryFormat::MachO
    } else if info.target.contains("windows") {
        object::BinaryFormat::Pe
    } else {
        object::BinaryFormat::Elf
    };
    if file.architecture() != architecture
        || file.format() != format
        || info.bundle_digest.is_none()
        || !matches!(info.source.state.as_str(), "committed" | "working-tree")
        || info.source.commit.len() != 40
        || !info.source.commit.bytes().all(|b| b.is_ascii_hexdigit())
    {
        return Err(invalid("程序目标或来源记录不合法"));
    }
    Ok(info)
}
pub fn read_binary_info(path: &Path) -> Result<BuildInfo> {
    let bytes = read_local(path, MAX_ASSET)?;
    binary_info(&bytes)
}
pub(crate) fn read_local(path: &Path, limit: u64) -> Result<Vec<u8>> {
    let path = fs::absolute(path)?;
    let root = Root::open(path.parent().ok_or_else(|| invalid("文件缺少父目录"))?)?;
    let name = RelPath::new(
        path.file_name()
            .and_then(|p| p.to_str())
            .ok_or_else(|| invalid("文件名不合法"))?,
    )?;
    Ok(root.read_checked(&name, limit)?.bytes)
}
pub fn package(binary: &Path, info: &ReleaseInfo, out: &Path) -> Result<PackageReport> {
    let bytes = read_local(binary, MAX_ASSET)?;
    let actual = binary_info(&bytes)?;
    if actual != *info {
        return Err(invalid("声明的版本、平台或来源与实际程序不一致"));
    }
    let name = RelPath::new(binary_name(&info.target)?)?;
    let manifest = ReleaseManifest {
        format: "yss.release".into(),
        format_version: 1,
        build: actual,
        payload: BTreeMap::from([(name.clone(), FileDescriptor::new(&bytes, 0o755))]),
    };
    let entries = BTreeMap::from([
        (name, Entry { bytes, mode: 0o755 }),
        (
            RelPath::new("release-manifest.json")?,
            Entry {
                bytes: codec::encode_json(&manifest)?,
                mode: 0o644,
            },
        ),
    ]);
    let bytes = if info.target.contains("windows") {
        pack_zip(&entries)?
    } else {
        archive::pack(&entries)?
    };
    let sha256 = Sha256::of(&bytes);
    verify(&bytes, &sha256, &info.target)?;
    let out = fs::absolute(out)?;
    if out.file_name().and_then(|n| n.to_str())
        != Some(filename(&info.version, &info.target)?.as_str())
    {
        return Err(invalid("发行归档名称必须与版本和 target 一致"));
    }
    let root = Root::open(out.parent().ok_or_else(|| invalid("归档缺少父目录"))?)?;
    let name = RelPath::new(
        out.file_name()
            .and_then(|p| p.to_str())
            .ok_or_else(|| invalid("归档名不合法"))?,
    )?;
    if root.inspect(&name)? != FileSnapshot::Missing {
        return Err(YssError::new(ErrorCode::Conflict, "输出归档已存在"));
    }
    let staging = RelPath::new(format!(".yss-package-{}", fs::unique_id()?))?;
    let owned = root.create_file(&staging, &bytes, 0o644)?;
    if let Err(error) = root.rename(&staging, &name, &FileSnapshot::Missing, false) {
        let _ = root.remove(&staging, &owned);
        return Err(error);
    }
    Ok(PackageReport {
        path: out.display().to_string(),
        sha256,
        bytes: bytes.len() as u64,
        manifest,
    })
}
pub fn verify(
    bytes: &[u8],
    expected_sha: &Sha256,
    expected_target: &str,
) -> Result<VerifiedRelease> {
    validate_target(expected_target)?;
    if bytes.len() as u64 > MAX_ASSET {
        return Err(invalid("归档超过 256 MiB"));
    }
    if Sha256::of(bytes) != *expected_sha {
        return Err(YssError::new(
            ErrorCode::DigestMismatch,
            "发行归档 SHA-256 不匹配",
        ));
    }
    let mut entries = if expected_target.contains("windows") {
        unpack_zip(bytes)?
    } else {
        archive::unpack(bytes)?
    };
    let entry = entries
        .remove(&RelPath::new("release-manifest.json")?)
        .ok_or_else(|| {
            YssError::new(
                ErrorCode::UnsupportedFormat,
                "缺少 Rust 发行清单；旧格式不受支持",
            )
        })?;
    if entry.mode != 0o644 {
        return Err(invalid("发行清单模式错误"));
    }
    let manifest: ReleaseManifest = codec::decode_json(&entry.bytes, Limits::manifest())?;
    codec::ensure_format(&manifest.format, manifest.format_version, "yss.release")?;
    if manifest.build.target != expected_target {
        return Err(YssError::new(
            ErrorCode::UnsupportedPlatform,
            "发行包 target 不匹配",
        ));
    }
    version(&manifest.build.version, false)?;
    let path = RelPath::new(binary_name(expected_target)?)?;
    if entries.len() != 1 || manifest.payload.len() != 1 {
        return Err(invalid("发行包只允许一个程序载荷"));
    }
    let entry = entries
        .remove(&path)
        .ok_or_else(|| invalid("缺少程序载荷"))?;
    let descriptor = FileDescriptor::new(&entry.bytes, entry.mode);
    if entry.mode != 0o755 || manifest.payload.get(&path) != Some(&descriptor) {
        return Err(YssError::new(
            ErrorCode::DigestMismatch,
            "程序载荷描述符不匹配",
        ));
    }
    if binary_info(&entry.bytes)? != manifest.build {
        return Err(invalid("包内来源与程序来源不同"));
    }
    let mut tree = RenderedTree::default();
    tree.insert(path, entry.bytes, 0o755)?;
    Ok(VerifiedRelease { manifest, tree })
}
fn pack_zip(entries: &BTreeMap<RelPath, Entry>) -> Result<Vec<u8>> {
    let mut archive = zip::ZipWriter::new(Cursor::new(Vec::new()));
    for (path, entry) in entries {
        archive
            .start_file(
                path.as_str(),
                zip::write::SimpleFileOptions::default()
                    .compression_method(zip::CompressionMethod::Deflated)
                    .unix_permissions(entry.mode),
            )
            .map_err(|_| invalid("ZIP 创建失败"))?;
        archive.write_all(&entry.bytes)?;
    }
    let bytes = archive
        .finish()
        .map_err(|_| invalid("ZIP 完成失败"))?
        .into_inner();
    if bytes.len() as u64 > MAX_ASSET {
        return Err(invalid("ZIP 归档超限"));
    }
    Ok(bytes)
}
fn unpack_zip(bytes: &[u8]) -> Result<BTreeMap<RelPath, Entry>> {
    // Release ZIP deliberately uses classic, single-disk, comment-free headers below 256 MiB.
    if bytes.len() < 22 || bytes.len() as u64 > MAX_ASSET {
        return Err(invalid("ZIP 长度不合法"));
    }
    let end = &bytes[bytes.len() - 22..];
    let u16_at = |p| u16::from_le_bytes([end[p], end[p + 1]]);
    let u32_at = |p| u32::from_le_bytes([end[p], end[p + 1], end[p + 2], end[p + 3]]) as u64;
    if &end[..4] != b"PK\x05\x06"
        || u16_at(4) != 0
        || u16_at(6) != 0
        || u16_at(8) != 2
        || u16_at(10) != 2
        || u16_at(20) != 0
        || u32_at(12) + u32_at(16) + 22 != bytes.len() as u64
    {
        return Err(invalid("ZIP 尾部、多卷或条目数不合法"));
    }
    let mut archive =
        zip::ZipArchive::new(Cursor::new(bytes)).map_err(|_| invalid("ZIP 结构不合法"))?;
    if archive.len() != 2
        || archive.offset() != 0
        || archive.has_duplicate_names()
        || archive
            .has_overlapping_entries()
            .map_err(|_| invalid("ZIP 条目不合法"))?
    {
        return Err(invalid("ZIP 含重复或重叠条目"));
    }
    archive
        .validate_header_consistency()
        .map_err(|_| invalid("ZIP 头不一致"))?;
    let mut entries = BTreeMap::new();
    let mut total = 0u64;
    let mut ranges = Vec::new();
    for i in 0..archive.len() {
        let mut file = archive
            .by_index(i)
            .map_err(|_| invalid("ZIP 条目无法读取"))?;
        let path = RelPath::new(
            std::str::from_utf8(file.name_raw()).map_err(|_| invalid("ZIP 名称不是 UTF-8"))?,
        )?;
        let mode = file
            .unix_mode()
            .ok_or_else(|| invalid("ZIP 缺少文件模式"))?;
        if file.encrypted()
            || mode & 0o170000 != 0o100000
            || !matches!(mode & 0o7777, 0o644 | 0o755)
            || !matches!(
                file.compression(),
                zip::CompressionMethod::Deflated | zip::CompressionMethod::Stored
            )
        {
            return Err(invalid("ZIP 不允许链接、特殊条目或加密"));
        }
        let size = file.size();
        let limit = if path.as_str() == "release-manifest.json" {
            MAX_MANIFEST
        } else {
            MAX_ASSET
        };
        total = total
            .checked_add(size)
            .ok_or_else(|| invalid("ZIP 长度溢出"))?;
        if size > limit || total > MAX_ASSET {
            return Err(invalid("ZIP 解压超限"));
        }
        let start = file.header_start() as usize;
        let data = file.data_start().ok_or_else(|| invalid("ZIP 缺少本地头"))?;
        if start + 8 > bytes.len()
            || u16::from_le_bytes([bytes[start + 6], bytes[start + 7]]) & !0x800 != 0
        {
            return Err(invalid("ZIP 不允许数据描述符或特殊标志"));
        }
        ranges.push((start as u64, data + file.compressed_size()));
        let mut output = Vec::new();
        (&mut file)
            .take(limit + 1)
            .read_to_end(&mut output)
            .map_err(|_| invalid("ZIP 解压或 CRC 校验失败"))?;
        if output.len() as u64 != size
            || entries
                .insert(
                    path,
                    Entry {
                        bytes: output,
                        mode: mode & 0o777,
                    },
                )
                .is_some()
        {
            return Err(invalid("ZIP 长度或路径重复"));
        }
    }
    ranges.sort();
    let mut end = 0;
    for (start, next) in ranges {
        if start != end {
            return Err(invalid("ZIP 含未声明数据"));
        }
        end = next;
    }
    if end != archive.central_directory_start() {
        return Err(invalid("ZIP 含未声明数据"));
    }
    Ok(entries)
}
