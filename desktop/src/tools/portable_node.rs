//! Verify and unpack the fixed Windows Node archive without touching global Node.
use sha2::{Digest, Sha256};
use std::{
    collections::BTreeSet,
    io::{Cursor, Read},
};
use yss::{
    fs::{FileSnapshot, Root},
    model::RelPath,
};
const SHA256: &str = "d2046d28c2c3dcd211ac5145c0bf30e2047ef9dfb306693c8c6e2eccd8b95fa5";
const PREFIX: &str = "node-v24.22.1-win-x64/";
const MAX_ENTRY: u64 = 100 * 1024 * 1024;
const MAX_TOTAL: u64 = 160 * 1024 * 1024;

pub(super) fn extract(bytes: Vec<u8>, root: &Root) -> Result<(), String> {
    if bytes.len() > 64 * 1024 * 1024 || format!("{:x}", Sha256::digest(&bytes)) != SHA256 {
        return Err("Node 压缩包摘要不匹配；未安装".into());
    }
    let mut archive = zip::ZipArchive::new(Cursor::new(bytes)).map_err(|_| "Node 压缩包无效")?;
    if archive.len() > 5000
        || archive.offset() != 0
        || archive.has_duplicate_names()
        || archive
            .has_overlapping_entries()
            .map_err(|_| "Node 压缩包条目无效")?
    {
        return Err("Node 压缩包条目重复、重叠或过多".into());
    }
    archive
        .validate_header_consistency()
        .map_err(|_| "Node 压缩包头信息不一致")?;
    let mut seen = BTreeSet::new();
    let mut total = 0u64;
    let runtime = RelPath::new("node-runtime").map_err(|_| "Node 暂存路径无效")?;
    root.create_dir(&runtime)
        .map_err(|_| "无法创建 Node 私有目录")?;
    for index in 0..archive.len() {
        let mut file = archive
            .by_index(index)
            .map_err(|_| "无法读取 Node 压缩包条目")?;
        if file.encrypted()
            || !matches!(
                file.compression(),
                zip::CompressionMethod::Stored | zip::CompressionMethod::Deflated
            )
            || file
                .unix_mode()
                .is_some_and(|m| matches!(m & 0o170000, 0o120000 | 0o060000 | 0o020000 | 0o010000))
        {
            return Err("Node 压缩包包含不支持的文件类型".into());
        }
        let name = std::str::from_utf8(file.name_raw()).map_err(|_| "Node 压缩包路径不是 UTF-8")?;
        let name = name.strip_prefix(PREFIX).ok_or("Node 压缩包根目录不匹配")?;
        let name = name.trim_end_matches('/');
        if name.is_empty() {
            continue;
        }
        let rel = RelPath::new(format!("node-runtime/{name}"))
            .map_err(|_| "Node 压缩包包含不安全路径")?;
        if !seen.insert(rel.as_str().to_ascii_lowercase()) {
            return Err("Node 压缩包包含重复路径".into());
        }
        for parent in rel.parents() {
            let parent = RelPath::new(parent).map_err(|_| "Node 父目录路径无效")?;
            match root.inspect(&parent).map_err(|_| "无法检查 Node 父目录")? {
                FileSnapshot::Missing => {
                    root.create_dir(&parent)
                        .map_err(|_| "无法创建 Node 父目录")?;
                }
                FileSnapshot::Directory { .. } => {}
                _ => return Err("Node 路径与现有文件冲突".into()),
            }
        }
        if file.is_dir() {
            match root.inspect(&rel).map_err(|_| "无法检查 Node 目录")? {
                FileSnapshot::Missing => {
                    root.create_dir(&rel).map_err(|_| "无法创建 Node 目录")?;
                }
                FileSnapshot::Directory { .. } => {}
                _ => return Err("Node 路径与现有文件冲突".into()),
            }
            continue;
        }
        total = total.checked_add(file.size()).ok_or("Node 解压大小溢出")?;
        if file.size() > MAX_ENTRY || total > MAX_TOTAL {
            return Err("Node 解压超过安全上限".into());
        }
        let mut output = Vec::new();
        (&mut file)
            .take(MAX_ENTRY + 1)
            .read_to_end(&mut output)
            .map_err(|_| "Node 解压或校验失败")?;
        if output.len() as u64 != file.size() {
            return Err("Node 解压文件长度不匹配".into());
        }
        root.create_file(&rel, &output, 0o600)
            .map_err(|_| "无法安全写入 Node 文件")?;
    }
    for name in [
        "node-runtime/node.exe",
        "node-runtime/node_modules/npm/bin/npm-cli.js",
    ] {
        root.read_checked_single_link(
            &RelPath::new(name).map_err(|_| "Node 入口路径无效")?,
            MAX_ENTRY,
        )
        .map_err(|_| "Node 入口缺失或不安全")?;
    }
    Ok(())
}
