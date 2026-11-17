use super::{
    jobs::{JobContext, JobManager, JobRecord},
    sources,
    store::Store,
};
use crate::{
    bundle::{self, Bundle},
    cancel::Cancellation,
    codec,
    error::{ErrorCode, Result, YssError},
    fs::{self, FileSnapshot, Root},
    model::{BuildInfo, InstallationKind, Profile, RelPath, Sha256},
    project::{self, ProjectRequest},
    scaffold::{self, GenerationContext},
    transaction, updater,
};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use std::{
    collections::{BTreeMap, BTreeSet},
    path::{Path, PathBuf},
    sync::{Arc, Mutex},
};

#[derive(Debug, Clone, Copy, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub enum EngineeringOperation {
    Doctor,
    SkillsList,
    SkillsEnsure,
    Init,
    Attach,
    Sync,
    ScaffoldDdd,
    ScaffoldMvc,
    ScaffoldFrontend,
    Recover,
    Upgrade,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct UpgradeOptions {
    pub source: Option<String>,
    pub to: Option<String>,
    pub artifact: Option<String>,
    pub sha256: Option<Sha256>,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct EngineeringRequest {
    pub operation: EngineeringOperation,
    pub root: String,
    pub profile: Option<Profile>,
    pub installation: Option<InstallationKind>,
    #[serde(default)]
    pub variables: BTreeMap<String, String>,
    #[serde(default)]
    pub skills: BTreeSet<String>,
    pub project_name: Option<String>,
    pub backend: Option<scaffold::ScaffoldInputs>,
    pub upgrade: Option<UpgradeOptions>,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct EngineeringPreview {
    pub id: String,
    pub request: EngineeringRequest,
    pub report: Value,
    pub fingerprint: String,
    pub bundle_digest: String,
    pub created_at: String,
    pub context: GenerationContext,
}
#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct ExecuteEngineering {
    pub preview_id: String,
    pub request_id: String,
}

fn invalid(message: &str) -> YssError {
    YssError::new(ErrorCode::InvalidInput, message)
}
fn encode<T: Serialize>(value: &T) -> Result<Value> {
    serde_json::to_value(value).map_err(|_| invalid("无法编码工程操作结果"))
}
fn lock(store: &Arc<Mutex<Store>>) -> Result<std::sync::MutexGuard<'_, Store>> {
    store.lock().map_err(|_| invalid("工作台存储锁异常"))
}
fn checked_root(path: &str, data_dir: &Path) -> Result<PathBuf> {
    let path = Path::new(path);
    if !path.is_absolute() {
        return Err(invalid("工程根目录必须为绝对路径"));
    }
    let path = fs::absolute(path)?;
    if path.starts_with(data_dir) || data_dir.starts_with(&path) {
        return Err(YssError::new(
            ErrorCode::UnsafePath,
            "工程操作不能覆盖工作台数据目录",
        ));
    }
    Ok(path)
}
fn field(name: &str, label: &str, kind: &str, required: bool, options: Value) -> Value {
    json!({"name":name,"label":label,"type":kind,"required":required,"options":options})
}
pub fn catalog(bundle_bytes: &[u8]) -> Result<Value> {
    let bundle = bundle::load(bundle_bytes)?;
    let root = field("root", "工程绝对路径", "path", true, json!([]));
    let profile = field(
        "profile",
        "工程类型",
        "select",
        true,
        json!(["spec", "backend", "frontend"]),
    );
    let installation = field(
        "installation",
        "安装内容",
        "select",
        false,
        json!(["project", "skills"]),
    );
    let variables = field(
        "variables",
        "项目变量",
        "object",
        false,
        json!(["projectName", "businessDomain", "teamSize"]),
    );
    let mut operations = Vec::new();
    for (id, label, mutating, fields) in [
        ("doctor", "检查工程", false, vec![root.clone()]),
        ("skillsList", "查看技能", false, vec![root.clone()]),
        (
            "skillsEnsure",
            "安装技能",
            true,
            vec![
                root.clone(),
                field("skills", "技能", "skills", true, json!([])),
            ],
        ),
        (
            "init",
            "初始化空目录",
            true,
            vec![
                root.clone(),
                profile.clone(),
                installation.clone(),
                variables.clone(),
            ],
        ),
        (
            "attach",
            "接入已有工程",
            true,
            vec![
                root.clone(),
                profile.clone(),
                installation.clone(),
                variables.clone(),
            ],
        ),
        (
            "sync",
            "同步工程",
            true,
            vec![root.clone(), installation, variables],
        ),
        (
            "scaffoldDdd",
            "生成 DDD 工程",
            true,
            vec![
                root.clone(),
                field("backend", "后端配方", "backend", true, json!([])),
            ],
        ),
        (
            "scaffoldMvc",
            "生成 MVC 工程",
            true,
            vec![
                root.clone(),
                field("backend", "后端配方", "backend", true, json!([])),
            ],
        ),
        (
            "scaffoldFrontend",
            "生成前端工程",
            true,
            vec![
                root.clone(),
                field("projectName", "工程名", "text", true, json!([])),
            ],
        ),
        ("recover", "恢复未完成事务", true, vec![root.clone()]),
        (
            "upgrade",
            "升级 YSS 工具",
            true,
            vec![
                root,
                field(
                    "upgrade",
                    "升级来源与版本",
                    "upgrade",
                    true,
                    json!(["github", "gitlab"]),
                ),
            ],
        ),
    ] {
        operations.push(json!({"id":id,"label":label,"mutating":mutating,"fields":fields}));
    }
    let mut profiles = BTreeMap::new();
    for profile in [Profile::Spec, Profile::Backend, Profile::Frontend] {
        profiles.insert(
            format!("{profile:?}").to_ascii_lowercase(),
            bundle.profile(profile)?.skills(),
        );
    }
    Ok(
        json!({"operations":operations,"scaffold":scaffold::catalog(&bundle)?,"skills":profiles,
        "bundleDigest":bundle.digest,"processVersion":BuildInfo::current().version}),
    )
}
pub fn inspect(bundle_bytes: &[u8], root: &str) -> Result<Value> {
    let path = Path::new(root);
    if !path.is_absolute() {
        return Err(invalid("工程根目录必须为绝对路径"));
    }
    let bundle = bundle::load(bundle_bytes)?;
    let doctor = project::doctor(path, &bundle);
    let skills = project::list_skills(path, &bundle);
    let tool = updater::doctor(path);
    let tracker = super::integrations::tracker(root)?;
    Ok(
        json!({"root":fs::absolute(path)?,"doctor":doctor.as_ref().ok(),"doctorError":doctor.err(),
        "skills":skills.as_ref().ok(),"skillsError":skills.err(),"tool":tool.as_ref().ok(),
        "toolError":tool.err(),"tracker":tracker,"processVersion":BuildInfo::current().version}),
    )
}
fn project_request(request: &EngineeringRequest, dry_run: bool) -> Result<ProjectRequest> {
    let root = PathBuf::from(&request.root);
    Ok(match request.operation {
        EngineeringOperation::Init | EngineeringOperation::Attach => {
            let install = project::InstallRequest {
                root,
                profile: request.profile.ok_or_else(|| invalid("请选择工程类型"))?,
                installation: request.installation,
                variables: request.variables.clone(),
                dry_run,
            };
            if matches!(request.operation, EngineeringOperation::Init) {
                ProjectRequest::Init(install)
            } else {
                ProjectRequest::Attach(install)
            }
        }
        EngineeringOperation::Sync => ProjectRequest::Sync(project::SyncRequest {
            root,
            profile: request.profile,
            installation: request.installation,
            variables: request.variables.clone(),
            dry_run,
        }),
        EngineeringOperation::SkillsEnsure => ProjectRequest::Ensure(project::EnsureRequest {
            root,
            profile: request.profile,
            skills: request.skills.clone(),
            dry_run,
        }),
        _ => return Err(invalid("此操作不生成工程安装计划")),
    })
}
fn backend_request(
    request: &EngineeringRequest,
    dry_run: bool,
) -> Result<scaffold::ScaffoldRequest> {
    Ok(scaffold::ScaffoldRequest {
        architecture: if matches!(request.operation, EngineeringOperation::ScaffoldDdd) {
            scaffold::Architecture::Ddd
        } else {
            scaffold::Architecture::Mvc
        },
        input: request
            .backend
            .clone()
            .ok_or_else(|| invalid("请选择并填写后端完整配方"))?,
        output_dir: PathBuf::from(&request.root),
        dry_run,
    })
}
fn frontend_request(
    request: &EngineeringRequest,
    dry_run: bool,
) -> Result<scaffold::frontend::FrontendRequest> {
    Ok(scaffold::frontend::FrontendRequest {
        input: scaffold::frontend::FrontendInputs {
            project_name: request
                .project_name
                .clone()
                .ok_or_else(|| invalid("请填写工程名"))?,
        },
        output_dir: PathBuf::from(&request.root),
        dry_run,
    })
}
fn upgrade_request(request: &EngineeringRequest, dry_run: bool) -> Result<updater::UpgradeRequest> {
    let options = request
        .upgrade
        .as_ref()
        .ok_or_else(|| invalid("请填写升级参数"))?;
    let source = match options.source.as_deref() {
        None => None,
        Some("github") => Some(crate::network::ReleaseSource::Github),
        Some("gitlab") => Some(crate::network::ReleaseSource::Gitlab),
        _ => return Err(invalid("升级来源不受支持")),
    };
    Ok(updater::UpgradeRequest {
        source,
        to: options.to.clone(),
        tool_root: Some(PathBuf::from(&request.root)),
        artifact: options.artifact.as_ref().map(PathBuf::from),
        sha256: options.sha256.clone(),
        dry_run,
    })
}
fn report(
    bundle: &Bundle,
    request: &EngineeringRequest,
    context: &GenerationContext,
    cancel: &Cancellation,
) -> Result<Value> {
    cancel.check()?;
    let path = Path::new(&request.root);
    match request.operation {
        EngineeringOperation::Doctor => encode(&project::doctor(path, bundle)?),
        EngineeringOperation::SkillsList => encode(&project::list_skills(path, bundle)?),
        EngineeringOperation::Init
        | EngineeringOperation::Attach
        | EngineeringOperation::Sync
        | EngineeringOperation::SkillsEnsure => {
            encode(&project::plan(bundle, &project_request(request, true)?)?.report)
        }
        EngineeringOperation::ScaffoldDdd | EngineeringOperation::ScaffoldMvc => {
            encode(&scaffold::generate_with_context(
                bundle,
                &backend_request(request, true)?,
                context,
                cancel,
            )?)
        }
        EngineeringOperation::ScaffoldFrontend => encode(&scaffold::frontend::generate(
            bundle,
            &frontend_request(request, true)?,
            cancel,
        )?),
        EngineeringOperation::Recover => encode(&transaction::recover(path, true, cancel)?),
        EngineeringOperation::Upgrade => encode(
            &updater::prepare(
                &upgrade_request(request, true)?,
                &BuildInfo::current(),
                cancel,
            )?
            .report,
        ),
    }
}
fn snapshot_tree(
    root: &Root,
    directory: &RelPath,
    out: &mut BTreeMap<String, FileSnapshot>,
) -> Result<()> {
    let snapshot = root.inspect(directory)?;
    out.insert(directory.as_str().into(), snapshot.clone());
    if matches!(snapshot, FileSnapshot::Directory { .. }) {
        for name in root.list(Some(directory))? {
            if out.len() > 40_000 {
                return Err(invalid("事务材料超过检查上限"));
            }
            snapshot_tree(root, &RelPath::new(format!("{directory}/{name}"))?, out)?;
        }
    }
    Ok(())
}
fn fingerprint(
    request: &EngineeringRequest,
    report: &Value,
    context: &GenerationContext,
    bundle: &Bundle,
) -> Result<String> {
    let path = Path::new(&request.root);
    let mut files = BTreeMap::new();
    let identity;
    if path.exists() {
        let root = Root::open(path)?;
        identity = encode(&root.identity)?;
        let mut paths = BTreeSet::from([
            ".yss/project.json".to_owned(),
            ".yss/tool.json".into(),
            ".yss/scaffold.json".into(),
        ]);
        for key in ["changes", "preserved"] {
            if let Some(items) = report.get(key).and_then(Value::as_array) {
                for item in items {
                    if let Some(path) = item
                        .as_str()
                        .or_else(|| item.get("path").and_then(Value::as_str))
                    {
                        paths.insert(path.into());
                    }
                }
            }
        }
        if let Ok((Some(state), _)) = project::state::detect(&root) {
            paths.extend(state.managed.keys().map(|p| p.as_str().to_owned()));
        }
        if let Some(name) = request
            .project_name
            .as_ref()
            .or_else(|| request.backend.as_ref().map(|b| &b.project_name))
        {
            paths.insert(name.clone());
        }
        if matches!(request.operation, EngineeringOperation::Upgrade) {
            paths.insert(updater::release::binary_name(&BuildInfo::current().target)?.into());
        }
        for path in paths {
            let path = RelPath::new(path)?;
            files.insert(path.as_str().into(), root.inspect(&path)?);
        }
        snapshot_tree(&root, &RelPath::new(".yss/transactions")?, &mut files)?;
        root.revalidate()?;
    } else {
        let parent = path
            .parent()
            .ok_or_else(|| invalid("工程根目录没有父目录"))?;
        let root = Root::open(parent)?;
        let name = RelPath::new(
            path.file_name()
                .and_then(|s| s.to_str())
                .ok_or_else(|| invalid("工程目录名无效"))?,
        )?;
        identity = encode(&root.identity)?;
        files.insert(name.as_str().into(), root.inspect(&name)?);
    }
    let artifact = request
        .upgrade
        .as_ref()
        .and_then(|u| u.artifact.as_ref())
        .map(|artifact| {
            let path = Path::new(artifact);
            let parent = Root::open(path.parent().ok_or_else(|| invalid("归档路径无父目录"))?)?;
            parent
                .read_checked(
                    &RelPath::new(
                        path.file_name()
                            .and_then(|n| n.to_str())
                            .ok_or_else(|| invalid("归档名无效"))?,
                    )?,
                    crate::model::MAX_ASSET,
                )
                .map(|f| f.snapshot)
        })
        .transpose()?;
    Ok(Sha256::of(&codec::encode_json(&json!({"request":request,"report":report,"context":context,"bundle":bundle.digest,"identity":identity,"files":files,"artifact":artifact}))?).as_str().into())
}
pub fn preview(
    store: &Arc<Mutex<Store>>,
    bundle_bytes: &[u8],
    mut request: EngineeringRequest,
) -> Result<EngineeringPreview> {
    let data_dir = lock(store)?.data_dir().to_owned();
    request.root = checked_root(&request.root, &data_dir)?
        .display()
        .to_string();
    let bundle = bundle::load(bundle_bytes)?;
    let context = GenerationContext::capture()?;
    let report = report(&bundle, &request, &context, &Cancellation::new())?;
    // Pin a moving online latest release to the exact previewed version.
    if matches!(request.operation, EngineeringOperation::Upgrade)
        && let Some(options) = &mut request.upgrade
        && options.artifact.is_none()
    {
        options.to = report
            .get("targetVersion")
            .and_then(Value::as_str)
            .map(str::to_owned);
    }
    let preview = EngineeringPreview {
        id: fs::unique_id()?,
        fingerprint: fingerprint(&request, &report, &context, &bundle)?,
        request,
        report,
        bundle_digest: bundle.digest.as_str().into(),
        created_at: sources::now(),
        context,
    };
    lock(store)?.put_record("engineeringPreview", &preview.id, &preview)?;
    Ok(preview)
}
fn execute(context: JobContext, bundle_bytes: &[u8], preview: EngineeringPreview) -> Result<Value> {
    let bundle = bundle::load(bundle_bytes)?;
    let data_dir = lock(&context.store)?.data_dir().to_owned();
    checked_root(&preview.request.root, &data_dir)?;
    context.cancel.check()?;
    let mut project_plan = if matches!(
        preview.request.operation,
        EngineeringOperation::Init
            | EngineeringOperation::Attach
            | EngineeringOperation::Sync
            | EngineeringOperation::SkillsEnsure
    ) {
        Some(project::plan(
            &bundle,
            &project_request(&preview.request, true)?,
        )?)
    } else {
        None
    };
    let current = match &project_plan {
        Some(plan) => encode(&plan.report)?,
        None => report(&bundle, &preview.request, &preview.context, &context.cancel)?,
    };
    if bundle.digest.as_str() != preview.bundle_digest
        || fingerprint(&preview.request, &current, &preview.context, &bundle)?
            != preview.fingerprint
    {
        return Err(YssError::new(
            ErrorCode::ConcurrentChange,
            "工程、Bundle、参数或文件已改变，请重新预览",
        ));
    }
    context.emit("plan", json!({"previewId":preview.id,"report":current}))?;
    context.set_status("running")?;
    let request = &preview.request;
    let result = match request.operation {
        EngineeringOperation::Doctor | EngineeringOperation::SkillsList => current,
        EngineeringOperation::Init
        | EngineeringOperation::Attach
        | EngineeringOperation::Sync
        | EngineeringOperation::SkillsEnsure => {
            let mut plan = project_plan.take().expect("prepared project plan");
            plan.report.dry_run = false;
            if matches!(
                request.operation,
                EngineeringOperation::Init | EngineeringOperation::Attach
            ) {
                plan.report.next_steps = crate::guidance::project_next_steps(
                    plan.report.profile,
                    plan.report.installation,
                );
            }
            encode(&project::execute(plan, &context.cancel)?)?
        }
        EngineeringOperation::ScaffoldDdd | EngineeringOperation::ScaffoldMvc => {
            encode(&scaffold::generate_with_context(
                &bundle,
                &backend_request(request, false)?,
                &preview.context,
                &context.cancel,
            )?)?
        }
        EngineeringOperation::ScaffoldFrontend => encode(&scaffold::frontend::generate(
            &bundle,
            &frontend_request(request, false)?,
            &context.cancel,
        )?)?,
        EngineeringOperation::Recover => encode(&transaction::recover(
            Path::new(&request.root),
            false,
            &context.cancel,
        )?)?,
        EngineeringOperation::Upgrade => {
            let plan = updater::prepare(
                &upgrade_request(request, false)?,
                &BuildInfo::current(),
                &context.cancel,
            )?;
            if plan.report.archive_sha256.as_str()
                != preview
                    .report
                    .get("archiveSha256")
                    .and_then(Value::as_str)
                    .unwrap_or("")
            {
                return Err(YssError::new(
                    ErrorCode::ConcurrentChange,
                    "升级发行摘要已改变，请重新预览",
                ));
            }
            if fingerprint(request, &current, &preview.context, &bundle)? != preview.fingerprint {
                return Err(YssError::new(
                    ErrorCode::ConcurrentChange,
                    "下载升级包期间安装状态已改变，请重新预览",
                ));
            }
            let report = updater::execute(plan, &context.cancel)?;
            let process_build = BuildInfo::current();
            let disk_build = updater::doctor(Path::new(&report.root))
                .ok()
                .and_then(|doctor| doctor.installation)
                .map(|receipt| receipt.build);
            let mut result = encode(&report)?;
            result["diskVersion"] = json!(report.target_version);
            result["processBuild"] = json!(process_build);
            result["diskBuild"] = json!(disk_build);
            result["restartRequired"] = json!(disk_build.as_ref() != Some(&process_build));
            result
        }
    };
    if result
        .pointer("/transaction/cleanup")
        .or_else(|| result.get("cleanup"))
        .and_then(Value::as_str)
        == Some("pending")
    {
        context.set_status("recoveryRequired")?;
    }
    Ok(result)
}
pub fn start(
    manager: Arc<JobManager>,
    bundle_bytes: Arc<[u8]>,
    request: ExecuteEngineering,
) -> Result<JobRecord> {
    let preview: EngineeringPreview = manager
        .store
        .lock()
        .map_err(|_| invalid("工作台存储锁异常"))?
        .record("engineeringPreview", &request.preview_id)?
        .ok_or_else(|| invalid("工程预览不存在，请重新预览"))?;
    let roots = vec![preview.request.root.clone()];
    manager.start(
        "engineering".into(),
        format!("{:?} · {}", preview.request.operation, preview.id),
        request.request_id,
        roots,
        move |context| execute(context, &bundle_bytes, preview),
    )
}
