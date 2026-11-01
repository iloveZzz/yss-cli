use super::{
    Action, PathChange, ProjectPlan, ProjectReport, ProjectRequest,
    state::{self, ManagedFile, ProjectState, STATE_PATH},
    unavailable_skills,
};
use crate::{
    bundle::Bundle,
    codec,
    error::{ErrorCode, Result, YssError},
    fs::{self, FileSnapshot, Root},
    model::{FileDescriptor, InstallationKind, RelPath, RenderedFile, RenderedTree},
    transaction::{self, ChangeSet, Operation},
};
use std::collections::{BTreeMap, BTreeSet};

pub fn plan(bundle: &Bundle, request: &ProjectRequest) -> Result<ProjectPlan> {
    let (path, requested_profile, requested_kind, variables, dry_run, new, init, ensure) =
        match request {
            ProjectRequest::Init(r) => (
                r.root.as_path(),
                Some(r.profile),
                r.installation,
                Some(&r.variables),
                r.dry_run,
                true,
                true,
                None,
            ),
            ProjectRequest::Attach(r) => (
                r.root.as_path(),
                Some(r.profile),
                r.installation,
                Some(&r.variables),
                r.dry_run,
                true,
                false,
                None,
            ),
            ProjectRequest::Sync(r) => (
                r.root.as_path(),
                r.profile,
                r.installation,
                Some(&r.variables),
                r.dry_run,
                false,
                false,
                None,
            ),
            ProjectRequest::Ensure(r) => (
                r.root.as_path(),
                r.profile,
                None,
                None,
                r.dry_run,
                false,
                false,
                Some(&r.skills),
            ),
        };
    let path = fs::absolute(path)?;
    let root = match std::fs::symlink_metadata(&path) {
        Ok(_) => Some(Root::open(&path)?),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound && init => None,
        Err(e) => return Err(e.into()),
    };
    let (previous, metadata_before) = if let Some(root) = &root {
        state::detect(root)?
    } else {
        (None, FileSnapshot::Missing)
    };
    if new && previous.is_some() {
        return Err(YssError::new(
            ErrorCode::Conflict,
            "项目已接入；请使用 sync",
        ));
    }
    if !new && previous.is_none() {
        return Err(YssError::new(
            ErrorCode::NotReady,
            "项目未接入；请先 init 或 attach",
        ));
    }
    if init
        && root
            .as_ref()
            .is_some_and(|r| r.list(None).is_ok_and(|l| !l.is_empty()))
    {
        return Err(YssError::new(
            ErrorCode::Conflict,
            "init 需要空目录；已有项目请使用 attach",
        ));
    }
    if let Some(root) = &root {
        if !transaction::status(&root.path)?.pending.is_empty() {
            return Err(YssError::new(
                ErrorCode::RecoveryRequired,
                "存在未完成事务；请先 recover",
            ));
        }
    } else {
        let parent = path
            .parent()
            .ok_or_else(|| YssError::new(ErrorCode::UnsafePath, "目标没有父目录"))?;
        let parent = Root::open(parent)?;
        let name = RelPath::new(
            path.file_name()
                .and_then(|s| s.to_str())
                .ok_or_else(|| YssError::new(ErrorCode::UnsafePath, "目标名称不合法"))?,
        )?;
        if parent.inspect(&name)? != FileSnapshot::Missing {
            return Err(YssError::new(ErrorCode::Conflict, "目标已存在"));
        }
    }
    let profile = previous
        .as_ref()
        .map(|s| s.profile)
        .or(requested_profile)
        .ok_or_else(|| YssError::new(ErrorCode::Argument, "新安装需要 --profile"))?;
    if requested_profile.is_some_and(|p| p != profile) {
        return Err(YssError::new(
            ErrorCode::Conflict,
            "所选 profile 与安装身份不同",
        ));
    }
    let installation = requested_kind
        .or_else(|| previous.as_ref().map(|s| s.installation))
        .unwrap_or(InstallationKind::Project);
    let profile_bundle = bundle.profile(profile)?;
    let mut explicit = previous
        .as_ref()
        .map(|s| s.explicit_skills.clone())
        .unwrap_or_default();
    if let Some(skills) = ensure {
        if skills.is_empty() {
            return Err(YssError::new(ErrorCode::Argument, "至少指定一个技能"));
        }
        let available = profile_bundle.skills();
        for name in skills {
            if let Some(retired) = profile_bundle.retired_skills.get(name) {
                return Err(YssError::new(
                    ErrorCode::SkillRetired,
                    format!(
                        "技能已退役：{}；替代建议：{}（未自动安装）",
                        retired.reason,
                        retired.replacements.join(", ")
                    ),
                )
                .at(name)
                .context("reason", &retired.reason)
                .context("replacements", retired.replacements.join(", ")));
            }
            if !available.contains(name) {
                return Err(YssError::new(
                    ErrorCode::InvalidInput,
                    "请求的技能不可用；未安装替代技能",
                )
                .at(name));
            }
        }
        explicit.extend(skills.iter().cloned());
    }
    let available = explicit
        .intersection(&profile_bundle.skills())
        .cloned()
        .collect();
    let diagnostics =
        unavailable_skills(profile_bundle, &explicit, previous.as_ref(), root.as_ref())?;
    let selection = bundle.select(profile, installation, &available)?;
    let mut vars = previous
        .as_ref()
        .map(|s| s.variables.clone())
        .unwrap_or_else(|| {
            BTreeMap::from([
                (
                    "projectName".into(),
                    path.file_name().unwrap().to_string_lossy().into_owned(),
                ),
                ("businessDomain".into(), "待补充".into()),
                ("teamSize".into(), "待补充".into()),
            ])
        });
    if let Some(variables) = variables {
        for (key, value) in variables {
            if !matches!(key.as_str(), "projectName" | "businessDomain" | "teamSize")
                || value.is_empty()
                || value.len() > 1024
                || value.chars().any(char::is_control)
                || value.contains("__YSS_")
            {
                return Err(YssError::new(ErrorCode::Argument, "项目变量名称或值不合法").at(key));
            }
            vars.insert(key.clone(), value.clone());
        }
    }
    let mut desired = BTreeMap::new();
    for (path, file) in selection.files {
        if path
            .as_str()
            .split('/')
            .next()
            .is_some_and(|s| s.eq_ignore_ascii_case(".yss"))
        {
            return Err(YssError::new(
                ErrorCode::InvalidInput,
                "模板不能覆盖 YSS 控制目录",
            ));
        }
        let bytes = if file.render {
            render(bundle.bytes(&file)?, &vars, path.as_str())?
        } else {
            bundle.bytes(&file)?.to_vec()
        };
        desired.insert(
            path,
            (
                file.ownership,
                RenderedFile {
                    bytes,
                    mode: fs::observable_mode(file.descriptor.mode),
                },
            ),
        );
    }
    let mut report = ProjectReport {
        root: path.display().to_string(),
        profile,
        installation,
        status: "planned".into(),
        dry_run,
        changes: Vec::new(),
        preserved: Vec::new(),
        diagnostics,
        transaction: None,
    };
    let old = previous
        .as_ref()
        .map(|s| s.managed.clone())
        .unwrap_or_default();
    let all: BTreeSet<_> = old.keys().chain(desired.keys()).cloned().collect();
    let mut managed = BTreeMap::new();
    let mut operations = Vec::new();
    let mut guards = BTreeMap::new();
    let mut conflicts = Vec::new();
    let mut tree = RenderedTree::default();
    for path in all {
        let current = if let Some(root) = &root {
            root.inspect(&path)?
        } else {
            FileSnapshot::Missing
        };
        guards.insert(path.clone(), current.clone());
        let previous = old.get(&path);
        let target = desired.get(&path);
        if let Some((ownership, file)) = target {
            let descriptor = FileDescriptor::new(&file.bytes, file.mode);
            if !ownership.is_managed() || previous.is_some_and(|p| !p.ownership.is_managed()) {
                if current == FileSnapshot::Missing && previous.is_none() {
                    add_create(&mut operations, &mut tree, &mut report, &path, file)?;
                    managed.insert(
                        path.clone(),
                        ManagedFile {
                            ownership: *ownership,
                            last_installed: descriptor,
                        },
                    );
                } else {
                    report.preserved.push(path.clone());
                    if let Some(previous) = previous {
                        managed.insert(path.clone(), previous.clone());
                    }
                }
            } else if let Some(previous) = previous {
                if current.descriptor() == Some(&previous.last_installed) {
                    if descriptor != previous.last_installed {
                        operations.push(Operation::Replace {
                            path: path.clone(),
                            before: current.clone(),
                            file: file.clone(),
                        });
                        report.changes.push(PathChange {
                            path: path.clone(),
                            action: "replace",
                        });
                    }
                    managed.insert(
                        path.clone(),
                        ManagedFile {
                            ownership: *ownership,
                            last_installed: descriptor,
                        },
                    );
                } else if descriptor == previous.last_installed {
                    report.preserved.push(path.clone());
                    managed.insert(path.clone(), previous.clone());
                } else {
                    conflicts.push(path.clone());
                }
            } else if current == FileSnapshot::Missing {
                add_create(&mut operations, &mut tree, &mut report, &path, file)?;
                managed.insert(
                    path.clone(),
                    ManagedFile {
                        ownership: *ownership,
                        last_installed: descriptor,
                    },
                );
            } else {
                conflicts.push(path.clone());
            }
        } else if let Some(previous) = previous {
            if !previous.ownership.is_managed() {
                report.preserved.push(path.clone());
            } else if current == FileSnapshot::Missing {
            } else if current.descriptor() == Some(&previous.last_installed) {
                operations.push(Operation::Delete {
                    path: path.clone(),
                    before: current,
                });
                report.changes.push(PathChange {
                    path: path.clone(),
                    action: "delete",
                });
            } else {
                conflicts.push(path.clone());
            }
        }
    }
    if !conflicts.is_empty() {
        let mut message = "本地内容与模板变更冲突；未写入文件".to_owned();
        for diagnostic in &report.diagnostics {
            message.push_str(&format!(
                "\n{}：{}；替代建议：{}",
                diagnostic.skill,
                diagnostic.reason,
                diagnostic.replacements.join(", ")
            ));
        }
        return Err(YssError::new(ErrorCode::Conflict, message)
            .context(
                "paths",
                conflicts
                    .iter()
                    .map(ToString::to_string)
                    .collect::<Vec<_>>()
                    .join("\n"),
            )
            .context(
                "diagnostics",
                String::from_utf8_lossy(&codec::encode_json(&report.diagnostics)?),
            ));
    }
    let state = ProjectState {
        format: "yss.project".into(),
        format_version: 1,
        profile,
        installation,
        variables: vars,
        source: bundle.manifest.source.clone(),
        bundle_digest: bundle.digest.clone(),
        explicit_skills: explicit,
        managed,
    };
    let bytes = codec::encode_json(&state)?;
    let metadata = RelPath::new(STATE_PATH)?;
    let descriptor = FileDescriptor::new(&bytes, fs::observable_mode(0o644));
    if operations.is_empty() && metadata_before.descriptor() == Some(&descriptor) {
        report.status = "unchanged".into();
        return Ok(ProjectPlan {
            root: path,
            report,
            action: Action::Unchanged,
        });
    }
    report.changes.push(PathChange {
        path: metadata.clone(),
        action: "record",
    });
    let action = if let Some(root) = &root {
        let mut directories = BTreeSet::new();
        for operation in &operations {
            if matches!(operation, Operation::Delete { .. }) {
                continue;
            }
            for parent in operation.path().parents() {
                let parent = RelPath::new(parent)?;
                match root.inspect(&parent)? {
                    FileSnapshot::Missing => {
                        directories.insert(parent);
                    }
                    FileSnapshot::Directory { .. } => {}
                    _ => {
                        return Err(
                            YssError::new(ErrorCode::Conflict, "目标父路径不是普通目录").at(parent)
                        );
                    }
                }
            }
        }
        let mut sorted: Vec<_> = directories
            .into_iter()
            .map(|path| Operation::CreateDir { path })
            .collect();
        sorted.extend(operations);
        guards.insert(metadata.clone(), metadata_before.clone());
        let file = RenderedFile { bytes, mode: 0o644 };
        let metadata = if metadata_before == FileSnapshot::Missing {
            Operation::Create {
                path: metadata,
                file,
            }
        } else {
            Operation::Replace {
                path: metadata,
                before: metadata_before,
                file,
            }
        };
        Action::Transaction(ChangeSet {
            root_identity: root.identity.clone(),
            operations: sorted,
            guards,
            metadata,
        })
    } else {
        tree.insert(metadata, bytes, 0o644)?;
        Action::New(tree)
    };
    Ok(ProjectPlan {
        root: path,
        report,
        action,
    })
}
fn add_create(
    ops: &mut Vec<Operation>,
    tree: &mut RenderedTree,
    report: &mut ProjectReport,
    path: &RelPath,
    file: &RenderedFile,
) -> Result<()> {
    ops.push(Operation::Create {
        path: path.clone(),
        file: file.clone(),
    });
    tree.insert(path.clone(), file.bytes.clone(), file.mode)?;
    report.changes.push(PathChange {
        path: path.clone(),
        action: "create",
    });
    Ok(())
}
fn render(bytes: &[u8], vars: &BTreeMap<String, String>, path: &str) -> Result<Vec<u8>> {
    fn replace(text: &str, vars: &BTreeMap<String, String>) -> String {
        let mut text = text.to_owned();
        for (key, token) in [
            ("projectName", "__YSS_PROJECT_NAME__"),
            ("businessDomain", "__YSS_BUSINESS_DOMAIN__"),
            ("teamSize", "__YSS_TEAM_SIZE__"),
        ] {
            if let Some(value) = vars.get(key) {
                text = text.replace(token, value);
            }
        }
        text
    }
    if path.ends_with(".json") {
        let mut value: serde_json::Value = codec::decode_json(bytes, codec::Limits::asset())?;
        fn walk(value: &mut serde_json::Value, vars: &BTreeMap<String, String>) {
            match value {
                serde_json::Value::String(s) => *s = replace(s, vars),
                serde_json::Value::Array(a) => a.iter_mut().for_each(|v| walk(v, vars)),
                serde_json::Value::Object(m) => m.values_mut().for_each(|v| walk(v, vars)),
                _ => {}
            }
        }
        walk(&mut value, vars);
        codec::encode_json(&value)
    } else {
        let text = std::str::from_utf8(bytes).map_err(|_| {
            YssError::new(ErrorCode::InvalidInput, "需要渲染的模板不是 UTF-8").at(path)
        })?;
        Ok(replace(text, vars).into_bytes())
    }
}
