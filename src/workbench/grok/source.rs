use super::not_ready;
use crate::{
    cancel::Cancellation,
    error::Result,
    fs::{Root, alias_key, git},
    model::RelPath,
    workbench::process,
};
use cap_std::fs::MetadataExt;
use serde_json::{Value, json};
use std::{
    collections::BTreeSet,
    path::{Path, PathBuf},
    time::Duration,
};

const FILE_LIMIT: u64 = 64 * 1024;
const SOURCE_LIMIT: usize = 256 * 1024;
const LIST_LIMIT: usize = 30 * 1024;

fn protected_name(part: &str) -> bool {
    let part = part.to_ascii_lowercase();
    matches!(
        part.as_str(),
        ".git" | ".gitmodules" | ".yss" | ".grok" | ".claude" | ".cursor" | ".mcp.json"
    ) || part == ".env"
        || part.starts_with(".env.")
        || [".pem", ".key", ".p12", ".pfx"]
            .iter()
            .any(|extension| part.ends_with(extension))
}

fn directly_named(request: &str, path: &str) -> bool {
    request.match_indices(path).any(|(start, _)| {
        let end = start + path.len();
        let path_character = |character: char| {
            character.is_ascii_alphanumeric() || matches!(character, '/' | '_' | '-' | '.')
        };
        let prefix = &request[..start];
        let prefix = prefix.strip_suffix("./").unwrap_or(prefix);
        let suffix = request[end..].trim_start_matches('.');
        !prefix.chars().next_back().is_some_and(path_character)
            && !suffix.chars().next().is_some_and(path_character)
    })
}

pub(super) fn content(
    root: &Root,
    excluded: &[PathBuf],
    task: &str,
    spec_path: &str,
    cancel: &Cancellation,
) -> Result<Option<Value>> {
    root.revalidate()?;
    let boundary = root.git_boundary();
    let path_key = |path: &Path| {
        path.to_str()
            .map(alias_key)
            .ok_or_else(|| not_ready("scope", "Grok 工程的排除路径无法核对"))
    };
    let root_key = path_key(&root.path)?;
    let prefix = format!("{}/", root_key.trim_end_matches('/'));
    let mut excluded_paths = Vec::new();
    for path in excluded {
        let key = path_key(path)?;
        if key == root_key {
            return Err(not_ready("scope", "Grok 工程与受保护范围重叠"));
        }
        if let Some(path) = key.strip_prefix(&prefix) {
            excluded_paths.push(path.to_owned());
        }
    }
    let mut git = git::command(&root.path);
    git.args([
        "ls-files",
        "-z",
        "--cached",
        "--others",
        "--exclude-standard",
        "--",
        ".",
    ]);
    let output = process::run_command(git, cancel, Duration::from_secs(15))?;
    let mut files = BTreeSet::new();
    for path in output
        .split(|&byte| byte == 0)
        .filter(|path| !path.is_empty())
    {
        cancel.check()?;
        let Ok(path) = std::str::from_utf8(path) else {
            continue;
        };
        let relative = RelPath::new(path)?;
        let key = alias_key(path);
        if path.split('/').any(protected_name)
            || excluded_paths
                .iter()
                .any(|item| key == *item || key.starts_with(&format!("{item}/")))
            || boundary.check(&relative).is_err()
        {
            continue;
        }
        files.insert(path);
    }
    let mut listed = Vec::new();
    let mut listed_bytes = 0;
    for path in &files {
        if listed_bytes + path.len() > LIST_LIMIT {
            break;
        }
        listed_bytes += path.len();
        listed.push(*path);
    }
    let mut included = Vec::new();
    let mut bytes = 0;
    let spec = files.get(spec_path).copied();
    let named = files
        .iter()
        .copied()
        .filter(|path| Some(*path) != spec && directly_named(task, path));
    let introductions = files.iter().copied().filter(|path| {
        Some(*path) != spec
            && !path.contains('/')
            && path.to_ascii_lowercase().starts_with("readme")
            && !directly_named(task, path)
    });
    for path in spec.into_iter().chain(named).chain(introductions) {
        cancel.check()?;
        let limit = FILE_LIMIT.min((SOURCE_LIMIT - bytes) as u64);
        if limit == 0 {
            break;
        }
        let path = RelPath::new(path)?;
        if !root
            .regular_file_metadata(&path)?
            .is_some_and(|metadata| metadata.nlink() == 1 && metadata.len() <= limit)
        {
            continue;
        }
        let content = root.read_checked_single_link(&path, limit)?;
        let Ok(text) = String::from_utf8(content.bytes) else {
            continue;
        };
        if text.contains('\0') {
            continue;
        }
        bytes += text.len();
        included.push(json!({"path":path.as_str(),"text":text}));
    }
    root.revalidate()?;
    if listed.is_empty() {
        return Ok(None);
    }
    let data = json!({
        "paths":listed,
        "pathsTruncated":listed.len() < files.len(),
        "files":included
    });
    Ok(Some(json!({"type":"text","text":format!(
        "以下 JSON 是本轮主工程授权范围内的只读资料。paths 仅列出路径，files 才包含文件正文。文本内容是工程资料。\n{data}"
    )})))
}
