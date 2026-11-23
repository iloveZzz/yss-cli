use super::model::*;
use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
    fs::{self, Root},
    model::{RelPath, Sha256},
};
use std::{collections::BTreeSet, path::Path};

pub fn now() -> String {
    time::OffsetDateTime::now_utc()
        .format(&time::format_description::well_known::Rfc3339)
        .expect("UTC format")
}
pub fn invalid(message: &str) -> YssError {
    YssError::new(ErrorCode::InvalidInput, message)
}
pub fn missing(message: &str) -> YssError {
    invalid(message).context("httpStatus", 404)
}
pub fn project<'a>(state: &'a WorkbenchState, id: &str) -> Result<&'a ProjectRecord> {
    state
        .projects
        .iter()
        .find(|p| p.id == id)
        .ok_or_else(|| missing("项目不存在"))
}
pub fn spec<'a>(state: &'a WorkbenchState, id: &str) -> Result<&'a SpecRecord> {
    state
        .specs
        .iter()
        .find(|s| s.id == id)
        .ok_or_else(|| missing("Spec 不存在"))
}
pub fn markdown_path(path: &str) -> Result<RelPath> {
    let rel = RelPath::new(path)?;
    if !path.to_ascii_lowercase().ends_with(".md") {
        return Err(invalid("主规格只支持 .md 文件"));
    }
    Ok(rel)
}
pub fn text(root: &Root, path: &str) -> Result<String> {
    let rel = markdown_path(path)?;
    let metadata = root.source_metadata(&rel)?;
    if !metadata.is_file() {
        return Err(YssError::new(ErrorCode::UnsafePath, "来源不是普通文件"));
    }
    if metadata.len() > MAX_SOURCE {
        return Err(invalid("Markdown 超过 2 MiB 读取上限").context("httpStatus", 413));
    }
    let file = root.source_read(&rel, MAX_SOURCE)?;
    String::from_utf8(file.bytes)
        .map_err(|_| YssError::new(ErrorCode::UnsupportedFormat, "Markdown 不是 UTF-8"))
}
fn modified(root: &Root, path: &str) -> Option<String> {
    let time = root
        .source_metadata(&RelPath::new(path).ok()?)
        .ok()?
        .modified()
        .ok()?
        .into_std();
    time::OffsetDateTime::from(time)
        .format(&time::format_description::well_known::Rfc3339)
        .ok()
}
pub fn title(text: &str, path: &str) -> String {
    text.lines()
        .find_map(|l| l.strip_prefix("# "))
        .map(str::trim)
        .filter(|s| !s.is_empty())
        .map(str::to_owned)
        .unwrap_or_else(|| {
            Path::new(path)
                .file_stem()
                .unwrap_or_default()
                .to_string_lossy()
                .into_owned()
        })
}
pub fn read_source(state: &WorkbenchState, spec_id: &str) -> Result<SourceDocument> {
    let s = spec(state, spec_id)?;
    let p = project(state, &s.project_id)?;
    let root = Root::open(Path::new(&p.root_path))?;
    let text = text(&root, &s.source_path)?;
    root.revalidate()?;
    Ok(SourceDocument {
        spec_id: s.id.clone(),
        source_path: s.source_path.clone(),
        digest: Sha256::of(text.as_bytes()).as_str().into(),
        text,
        read_at: now(),
    })
}
fn health(error: &YssError) -> SourceHealth {
    if error.context.get("kind").is_some_and(|s| s == "NotFound") {
        SourceHealth::Missing
    } else if error.context.get("httpStatus").is_some_and(|s| s == "413") {
        SourceHealth::Oversized
    } else if matches!(
        error.code,
        ErrorCode::UnsafePath | ErrorCode::UnsupportedFormat | ErrorCode::InvalidInput
    ) {
        SourceHealth::Unsupported
    } else {
        SourceHealth::Unreadable
    }
}
pub(super) fn spec_view(
    state: &WorkbenchState,
    s: &SpecRecord,
    root: &Result<Root>,
    context: Option<&super::materials::Context<'_>>,
) -> SpecView {
    let read = match root {
        Ok(root) => text(root, &s.source_path),
        Err(e) => Err(e.clone()),
    };
    let (source_health, source_message) = match &read {
        Ok(_) => (SourceHealth::Available, None),
        Err(e) => (health(e), Some(e.message.clone())),
    };
    let meta = read
        .as_ref()
        .ok()
        .and_then(|text| super::content::metadata(text).ok().flatten());
    let resources = resource_views(
        &super::content::resolved_resources(s, meta.as_ref(), root.as_ref().ok()),
        root,
    );
    SpecView {
        progress: None,
        content: super::content::inspect(state, s, root, read.as_deref().ok(), context, false).0,
        record: s.clone(),
        source_health,
        source_message,
        source_modified_at: root.as_ref().ok().and_then(|r| modified(r, &s.source_path)),
        resources,
    }
}
pub fn resource_views(resources: &[ResourceRef], root: &Result<Root>) -> Vec<ResourceView> {
    resources
        .iter()
        .map(|r| {
            if r.target_type == TargetType::Web {
                return ResourceView {
                    record: r.clone(),
                    health: ResourceHealth::External,
                    message: Some("外部链接，未探测".into()),
                };
            }
            let result = match root {
                Ok(root) if r.kind == ResourceKind::Prototype => {
                    super::preview::check_entry(root, &r.target)
                }
                Ok(root) => RelPath::new(&r.target)
                    .and_then(|p| root.source_metadata(&p))
                    .and_then(|m| {
                        if m.is_file() {
                            Ok(())
                        } else {
                            Err(invalid("不是普通文件"))
                        }
                    }),
                Err(e) => Err(e.clone()),
            };
            let (h, m) = match result {
                Ok(()) => (ResourceHealth::Available, None),
                Err(e) => (
                    match health(&e) {
                        SourceHealth::Missing => ResourceHealth::Missing,
                        SourceHealth::Unsupported => ResourceHealth::Unsupported,
                        _ => ResourceHealth::Unreadable,
                    },
                    Some(e.message),
                ),
            };
            ResourceView {
                record: r.clone(),
                health: h,
                message: m,
            }
        })
        .collect()
}
pub fn source_directories(root: &Root, cancel: &Cancellation) -> Vec<RelPath> {
    ["docs/spec", "docs/specs", "specs", ".work"]
        .into_iter()
        .map(|path| RelPath::new(path).expect("fixed Spec directory"))
        .filter(|path| root.source_entries(Some(path), &mut 0, cancel).is_ok())
        .collect()
}

pub fn discover(
    state: &WorkbenchState,
    request: &DiscoverRequest,
    cancel: &Cancellation,
) -> Result<DiscoverResult> {
    let p = project(state, &request.project_id)?;
    let root = Root::open(Path::new(&p.root_path))?;
    scan(state, p, &root, request.directory.as_deref(), false, cancel)
}

pub(super) fn scan(
    state: &WorkbenchState,
    project: &ProjectRecord,
    root: &Root,
    directory: Option<&str>,
    managed_only: bool,
    cancel: &Cancellation,
) -> Result<DiscoverResult> {
    let registered: BTreeSet<_> = state
        .specs
        .iter()
        .filter(|spec| spec.project_id == project.id)
        .map(|spec| fs::alias_key(&spec.source_path))
        .collect();
    let mut stack = match directory {
        Some("" | ".") => vec![(None, 0)],
        Some(path) => vec![(Some(RelPath::new(path)?), 0)],
        None => source_directories(root, cancel)
            .into_iter()
            .rev()
            .map(|path| (Some(path), 0))
            .collect(),
    };
    if stack.is_empty() {
        stack.push((None, 0));
    }
    let mut budget = 10000;
    let mut result = DiscoverResult {
        candidates: vec![],
        warnings: vec![],
        complete: true,
    };
    while let Some((path, depth)) = stack.pop() {
        cancel.check()?;
        let (entries, complete) = match root.source_entries(path.as_ref(), &mut budget, cancel) {
            Ok(v) => v,
            Err(e) => {
                if e.code == ErrorCode::Cancelled {
                    return Err(e);
                }
                result.complete = false;
                result.warnings.push(format!(
                    "{}：{}",
                    path.as_ref().map(|p| p.as_str()).unwrap_or("."),
                    e.message
                ));
                continue;
            }
        };
        for (name, metadata) in entries {
            cancel.check()?;
            if [
                ".git",
                ".yss",
                ".agents",
                ".workbuddy",
                ".template-spec",
                ".template-source",
                "node_modules",
                "target",
                "dist",
                "build",
            ]
            .iter()
            .any(|s| name.eq_ignore_ascii_case(s))
            {
                continue;
            }
            let rel = match &path {
                Some(p) => p.join(&name),
                None => RelPath::new(&name),
            };
            let rel = match rel {
                Ok(p) => p,
                Err(e) => {
                    result.complete = false;
                    result.warnings.push(e.message);
                    continue;
                }
            };
            if fs::platform::dangerous(&metadata) {
                result
                    .warnings
                    .push(format!("{}：已跳过链接或特殊文件", rel));
                continue;
            }
            if metadata.is_dir() {
                if depth >= 16 {
                    result.complete = false;
                    result.warnings.push(format!("{}：超过 16 层扫描深度", rel));
                } else {
                    stack.push((Some(rel), depth + 1));
                }
            } else if name.to_ascii_lowercase().ends_with(".md") {
                let key = fs::alias_key(rel.as_str());
                if managed_only && registered.contains(&key) {
                    continue;
                }
                match candidate(root, &rel, managed_only) {
                    Ok(Some(content)) => result.candidates.push(Candidate {
                        source_path: rel.as_str().into(),
                        title: title(&content, rel.as_str()),
                        modified_at: if managed_only {
                            None
                        } else {
                            modified(root, rel.as_str())
                        },
                        already_registered: registered.contains(&key),
                    }),
                    Ok(None) => {}
                    Err(error) => {
                        if error.code == ErrorCode::Cancelled {
                            return Err(error);
                        }
                        result.complete = false;
                        result.warnings.push(format!("{}：{}", rel, error.message));
                    }
                }
            }
        }
        if !complete || (budget == 0 && !stack.is_empty()) {
            result.complete = false;
            result
                .warnings
                .push("已达到 10,000 个目录项上限，请缩小目录范围".into());
            break;
        }
    }
    root.revalidate()?;
    result
        .candidates
        .sort_by(|a, b| a.source_path.cmp(&b.source_path));
    Ok(result)
}

pub(super) fn candidate(root: &Root, path: &RelPath, managed_only: bool) -> Result<Option<String>> {
    let content = text(root, path.as_str())?;
    if managed_only {
        let name = path.as_str().rsplit('/').next().unwrap_or_default();
        let conventional =
            name.eq_ignore_ascii_case("spec.md") || name.to_ascii_lowercase().ends_with("-spec.md");
        let header = content
            .trim_start_matches('\u{feff}')
            .lines()
            .map(str::trim)
            .find(|line| !line.is_empty() && !line.starts_with("# "));
        if !conventional && !header.is_some_and(|line| line.starts_with("<!-- yss-spec ")) {
            return Ok(None);
        }
    }
    let main = super::registration::main_source_for_text(root, path.as_str(), &content)?;
    Ok((fs::alias_key(&main) == fs::alias_key(path.as_str())).then_some(content))
}
