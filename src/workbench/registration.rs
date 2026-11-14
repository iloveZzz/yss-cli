use super::{
    content,
    history::{self, EventTarget},
    model::*,
    sources::{self, invalid},
    store::Store,
};
use crate::{
    error::Result,
    fs::{self, Root},
    model::RelPath,
};
use std::path::Path;

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
pub fn main_source(root: &Root, source: &str) -> Result<String> {
    if content::metadata(&sources::text(root, source)?)?.is_some() {
        return Ok(source.into());
    }
    let sibling = local_link(source, "spec.md")?;
    if root
        .source_metadata(&RelPath::new(&sibling)?)
        .is_ok_and(|m| m.is_file())
    {
        Ok(sibling)
    } else {
        Ok(source.into())
    }
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
    let resources = content::merged_resources(&after, meta.as_ref());
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
    // 资源引用不代表归属；只合并没有独立 Spec 元数据的同目录交互说明。
    let interaction = fs::alias_key(&local_link(&before.source_path, "interaction.md")?);
    let children: Vec<_> = state
        .specs
        .iter()
        .filter(|s| {
            s.id != before.id
                && s.project_id == before.project_id
                && s.active
                && fs::alias_key(&s.source_path) == interaction
                && sources::text(&root, &s.source_path)
                    .and_then(|text| content::metadata(&text))
                    .is_ok_and(|meta| meta.is_none())
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
        let spec = SpecRecord {
            id: fs::unique_id()?,
            project_id: project_id.clone(),
            title: sources::title(&text, &source_path),
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
            created_at: at.clone(),
            updated_at: at.clone(),
            last_visited_at: None,
        };
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
