use super::{
    content,
    history::{self, EventTarget},
    model::*,
    sources::{self, invalid},
    store::Store,
};
use crate::{
    cancel::Cancellation,
    error::Result,
    fs::{self, Root},
    model::RelPath,
};
use std::{collections::BTreeSet, io::Write, path::Path};

pub fn local_link(base: &str, link: &str) -> Result<String> {
    if link.contains(':') || link.starts_with('/') || link.contains('\\') {
        return Err(invalid("仅支持项目内的相对文档链接"));
    }
    let link = link.split(['#', '?']).next().unwrap_or("");
    let decoded = percent_decode(link)?;
    let parent = Path::new(base).parent().unwrap_or_else(|| Path::new(""));
    let mut components: Vec<_> = parent
        .to_str()
        .unwrap_or("")
        .split('/')
        .filter(|p| !p.is_empty())
        .map(str::to_owned)
        .collect();
    for part in decoded.split('/') {
        match part {
            "" | "." => {}
            ".." => {
                if components.pop().is_none() {
                    return Err(invalid("链接超出项目目录"));
                }
            }
            _ => components.push(part.into()),
        }
    }
    let path = components.join("/");
    RelPath::new(&path)?;
    Ok(path)
}
fn percent_decode(value: &str) -> Result<String> {
    let bytes = value.as_bytes();
    let mut out = Vec::new();
    let mut i = 0;
    while i < bytes.len() {
        if bytes[i] == b'%' {
            let hex = value
                .get(i + 1..i + 3)
                .ok_or_else(|| invalid("链接编码无效"))?;
            out.push(u8::from_str_radix(hex, 16).map_err(|_| invalid("链接编码无效"))?);
            i += 3;
        } else {
            out.push(bytes[i]);
            i += 1;
        }
    }
    String::from_utf8(out).map_err(|_| invalid("链接不是 UTF-8"))
}
pub fn links(source: &str, text: &str) -> Vec<String> {
    text.split("](")
        .skip(1)
        .filter_map(|part| part.split(')').next())
        .filter_map(|part| {
            let path = part.trim().trim_start_matches('<').trim_end_matches('>');
            local_link(source, path).ok()
        })
        .collect()
}
fn inferred(root: &Root, source: &str, text: &str) -> Vec<ResourceRef> {
    let mut targets = links(source, text);
    if let Ok(path) = local_link(source, "interaction.md") {
        targets.push(path);
    }
    let mut resources = vec![];
    for target in targets {
        if target == source || resources.iter().any(|r: &ResourceRef| r.target == target) {
            continue;
        }
        if !RelPath::new(&target)
            .and_then(|p| root.source_metadata(&p))
            .is_ok_and(|m| m.is_file())
        {
            continue;
        }
        let kind = if target.ends_with(".html") {
            ResourceKind::Prototype
        } else if target.ends_with("interaction.md") {
            ResourceKind::Interaction
        } else if target.ends_with(".md") {
            ResourceKind::Supplement
        } else {
            continue;
        };
        resources.push(ResourceRef {
            label: Path::new(&target)
                .file_name()
                .unwrap_or_default()
                .to_string_lossy()
                .into(),
            kind,
            target_type: TargetType::Local,
            target,
        });
    }
    resources
}
fn attachment_main(source: &str) -> Option<String> {
    let (directory, name) = source.rsplit_once('/').unwrap_or(("", source));
    let lower = name.to_ascii_lowercase();
    let main = if lower == "interaction.md" {
        "spec.md".into()
    } else {
        let suffix = ["-interaction.md", "-design.md"]
            .into_iter()
            .find(|suffix| lower.ends_with(suffix))?;
        let key = &name[..name.len() - suffix.len()];
        if key.is_empty() {
            return None;
        }
        format!("{key}-spec.md")
    };
    Some(if directory.is_empty() {
        main
    } else {
        format!("{directory}/{main}")
    })
}
pub fn main_source(root: &Root, source: &str) -> Result<String> {
    main_source_for_text(root, source, &sources::text(root, source)?)
}
pub(super) fn main_source_for_text(root: &Root, source: &str, text: &str) -> Result<String> {
    if content::metadata(text)?.is_some() {
        return Ok(source.into());
    }
    let Some(sibling) = attachment_main(source) else {
        return Ok(source.into());
    };
    let text = match sources::text(root, &sibling) {
        Ok(text) => text,
        Err(error)
            if error
                .context
                .get("kind")
                .is_some_and(|kind| kind == "NotFound") =>
        {
            return Ok(source.into());
        }
        Err(error) => return Err(error),
    };
    let name = Path::new(source)
        .file_name()
        .and_then(|name| name.to_str())
        .unwrap_or_default()
        .to_ascii_lowercase();
    let legacy = name == "interaction.md";
    let owns_source = match content::metadata(&text)? {
        None => legacy,
        Some(meta) => meta.resources.iter().any(|resource| {
            resource.target_type == TargetType::Local
                && fs::alias_key(&resource.target) == fs::alias_key(source)
                && (legacy
                    || if name.ends_with("-interaction.md") {
                        resource.kind == ResourceKind::Interaction
                    } else {
                        matches!(
                            resource.kind,
                            ResourceKind::Supplement | ResourceKind::Other
                        )
                    })
        }),
    };
    Ok(if owns_source { sibling } else { source.into() })
}

pub(super) fn new_record(
    project_id: &str,
    source_path: String,
    title: String,
    at: &str,
) -> Result<SpecRecord> {
    Ok(SpecRecord {
        id: fs::unique_id()?,
        project_id: project_id.into(),
        title,
        source_path,
        stage: Stage::Unmarked,
        status: Status::Unmarked,
        next_action: String::new(),
        blocker: String::new(),
        notes: String::new(),
        resources: vec![],
        source_resource_targets: vec![],
        inferred_resources: vec![],
        active: true,
        created_at: at.into(),
        updated_at: at.into(),
        last_visited_at: None,
    })
}

pub(super) fn scan_project(
    state: &mut WorkbenchState,
    project: &ProjectRecord,
    at: &str,
    cancel: &Cancellation,
) -> Result<(usize, Vec<String>)> {
    let root = Root::open(Path::new(&project.root_path))?;
    let scan = sources::scan(state, project, &root, None, true, cancel)?;
    let mut warnings = scan.warnings;
    let mut found = Vec::new();
    let mut paths = BTreeSet::new();
    for candidate in scan.candidates {
        cancel.check()?;
        let key = fs::alias_key(&candidate.source_path);
        if !paths.insert(key) {
            warnings.push(format!("{}：路径与另一候选重复", candidate.source_path));
            continue;
        }
        let path = RelPath::new(&candidate.source_path)?;
        let text = match sources::candidate(&root, &path, true) {
            Ok(Some(text)) => text,
            Ok(None) => continue,
            Err(error) => {
                warnings.push(format!("{}：{}", candidate.source_path, error.message));
                continue;
            }
        };
        let title = sources::title(&text, &candidate.source_path);
        let mut spec = new_record(&project.id, candidate.source_path, title, at)?;
        if content::metadata(&text)?.is_none() {
            spec.inferred_resources = inferred(&root, &spec.source_path, &text);
        }
        found.push(spec);
    }
    root.revalidate()?;
    let added = found.len();
    for spec in found {
        history::record_change(
            state,
            EventTarget {
                project_id: spec.project_id.clone(),
                spec_id: Some(spec.id.clone()),
                task_id: None,
                lifecycle_stage: None,
            },
            None,
            &spec,
            at,
        )?;
        state.specs.push(spec);
    }
    Ok((added, warnings))
}

pub(super) fn scan_startup(
    store: &mut Store,
    cancel: &Cancellation,
    output: &mut dyn Write,
) -> Result<()> {
    let mut state = store.state().clone();
    let projects: Vec<_> = state
        .projects
        .iter()
        .filter(|project| project.active)
        .cloned()
        .collect();
    let at = sources::now();
    let mut added = 0;
    for project in &projects {
        cancel.check()?;
        match scan_project(&mut state, project, &at, cancel) {
            Ok((count, warnings)) => {
                added += count;
                if !warnings.is_empty() {
                    writeln!(
                        output,
                        "规格扫描提示：{} 有 {} 项提示，首项为 {}",
                        project.name,
                        warnings.len(),
                        warnings[0]
                    )?;
                }
            }
            Err(error) if error.code == crate::error::ErrorCode::Cancelled => return Err(error),
            Err(error) => writeln!(output, "规格扫描已跳过 {}：{}", project.name, error.message)?,
        }
    }
    cancel.check()?;
    if added != 0 {
        store.commit(state.revision, state)?;
    }
    if !projects.is_empty() {
        writeln!(
            output,
            "启动扫描：{} 个启用项目，新增 {} 个 Spec。",
            projects.len(),
            added
        )?;
    }
    Ok(())
}

pub fn refresh(state: &mut WorkbenchState, spec_id: &str, at: &str) -> Result<()> {
    let before = sources::spec(state, spec_id)?.clone();
    let project = sources::project(state, &before.project_id)?;
    let root = Root::open(Path::new(&project.root_path))?;
    let text = sources::text(&root, &before.source_path)?;
    let meta = content::metadata(&text)?;
    let mut after = before.clone();
    after.resources.retain(|resource| {
        !before
            .source_resource_targets
            .contains(&content::resource_key(resource))
    });
    after.source_resource_targets.clear();
    after.inferred_resources = if meta.is_none() {
        inferred(&root, &before.source_path, &text)
    } else {
        vec![]
    };
    let resources = content::resolved_resources(&after, meta.as_ref(), Some(&root));
    root.revalidate()?;
    let target = EventTarget {
        project_id: before.project_id.clone(),
        spec_id: Some(before.id.clone()),
        task_id: None,
        lifecycle_stage: None,
    };
    if history::record_change(state, target, Some(&before), &after, at)? {
        after.updated_at = at.into();
    }
    *state
        .specs
        .iter_mut()
        .find(|s| s.id == before.id)
        .expect("spec") = after.clone();
    // 先限定精确配对，再复用来源归属；个人资源引用不能停用独立 Spec。
    let main = fs::alias_key(&before.source_path);
    let children: Vec<_> = state
        .specs
        .iter()
        .filter(|s| {
            s.id != before.id
                && s.project_id == before.project_id
                && s.active
                && attachment_main(&s.source_path)
                    .is_some_and(|source| fs::alias_key(&source) == main)
                && main_source(&root, &s.source_path)
                    .is_ok_and(|source| fs::alias_key(&source) == main)
                && resources.iter().any(|r| {
                    r.target_type == TargetType::Local
                        && fs::alias_key(&r.target) == fs::alias_key(&s.source_path)
                })
        })
        .cloned()
        .collect();
    root.revalidate()?;
    for child in children {
        let mut inactive = child.clone();
        inactive.active = false;
        inactive.updated_at = at.into();
        history::record_change(
            state,
            EventTarget {
                project_id: child.project_id.clone(),
                spec_id: Some(child.id.clone()),
                task_id: None,
                lifecycle_stage: None,
            },
            Some(&child),
            &inactive,
            at,
        )?;
        *state
            .specs
            .iter_mut()
            .find(|s| s.id == child.id)
            .expect("child") = inactive;
    }
    Ok(())
}
pub fn register(
    store: &mut Store,
    root_path: &str,
    source_path: &str,
    expected_revision: u64,
) -> Result<MutationResult> {
    if store.state().revision != expected_revision {
        return Err(crate::error::YssError::new(
            crate::error::ErrorCode::ConcurrentChange,
            "内容已改变，请刷新后重新登记",
        )
        .context("revision", store.state().revision));
    }
    let path = super::service::project_root(root_path, store.data_dir())?;
    let root = Root::open(Path::new(&path))?;
    let source_path = main_source(&root, source_path)?;
    let text = sources::text(&root, &source_path)?;
    content::metadata(&text)?;
    root.revalidate()?;
    let mut state = store.state().clone();
    let at = sources::now();
    let project_id = if let Some(p) = state
        .projects
        .iter()
        .find(|p| fs::alias_key(&p.root_path) == fs::alias_key(&path))
    {
        p.id.clone()
    } else {
        super::jobs::ensure_grok_roots_available(store, std::slice::from_ref(&path))?;
        let project = ProjectRecord {
            id: fs::unique_id()?,
            name: Path::new(&path)
                .file_name()
                .unwrap_or_default()
                .to_string_lossy()
                .into(),
            root_path: path,
            active: true,
            created_at: at.clone(),
            updated_at: at.clone(),
        };
        history::record_change(
            &mut state,
            EventTarget {
                project_id: project.id.clone(),
                spec_id: None,
                task_id: None,
                lifecycle_stage: None,
            },
            None,
            &project,
            &at,
        )?;
        let id = project.id.clone();
        state.projects.push(project);
        id
    };
    if !sources::project(&state, &project_id)?.active {
        return Err(invalid("请先恢复项目"));
    }
    let spec_id = if let Some(s) = state.specs.iter().find(|s| {
        s.project_id == project_id && fs::alias_key(&s.source_path) == fs::alias_key(&source_path)
    }) {
        s.id.clone()
    } else {
        let title = sources::title(&text, &source_path);
        let spec = new_record(&project_id, source_path, title, &at)?;
        history::record_change(
            &mut state,
            EventTarget {
                project_id: project_id.clone(),
                spec_id: Some(spec.id.clone()),
                task_id: None,
                lifecycle_stage: None,
            },
            None,
            &spec,
            &at,
        )?;
        let id = spec.id.clone();
        state.specs.push(spec);
        id
    };
    refresh(&mut state, &spec_id, &at)?;
    let revision = if &state != store.state() {
        store.commit(expected_revision, state)?
    } else {
        expected_revision
    };
    Ok(MutationResult {
        response_id: None,
        revision,
        project_id: Some(project_id),
        spec_id: Some(spec_id),
        task_ids: vec![],
        view_id: None,
    })
}

pub fn read_document(state: &WorkbenchState, spec_id: &str, path: &str) -> Result<SourceDocument> {
    let spec = sources::spec(state, spec_id)?;
    let project = sources::project(state, &spec.project_id)?;
    let root = Root::open(Path::new(&project.root_path))?;
    let text = sources::text(&root, path)?;
    root.revalidate()?;
    Ok(SourceDocument {
        spec_id: spec_id.into(),
        source_path: path.into(),
        digest: crate::model::Sha256::of(text.as_bytes()).as_str().into(),
        text,
        read_at: sources::now(),
    })
}
