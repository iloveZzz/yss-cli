use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
    model::{BuildInfo, InstallationKind, Profile},
    output::{self, OutputEnvelope},
};
use clap::{Args, CommandFactory, Parser, Subcommand};
use serde::Serialize;
use std::{ffi::OsString, io::Write, path::PathBuf};

#[derive(Parser)]
#[command(
    name = "yss",
    about = "安装项目模板、管理技能并生成 Java / Vue 工程",
    after_help = "常用示例：\n  yss init --root ./my-project --profile spec --dry-run\n  yss skills list --root ./my-project\n  yss help init\n\n写入命令直接执行；添加 --dry-run 可先预览。",
    disable_version_flag = true
)]
pub struct Cli {
    #[arg(long,global=true,action=clap::ArgAction::Set,num_args=0..=1,default_missing_value="true",default_value="false",require_equals=true,help="以统一 JSON 信封输出结果")]
    pub json: bool,
    #[arg(
        short = 'V',
        long,
        global = true,
        help = "显示版本与构建来源；建议单独运行 yss --version"
    )]
    pub version: bool,
    #[command(subcommand)]
    pub command: Option<Command>,
}
#[derive(Subcommand)]
pub enum Command {
    #[command(about = "启动个人本地 Spec 工作台，或读取快照")]
    Workbench(WorkbenchArgs),
    #[command(
        about = "初始化新目录或空项目目录",
        after_help = "示例：\n  yss init --root ./my-project --profile spec --dry-run\n\n确认预览后去掉 --dry-run 执行；已有项目使用 yss attach。"
    )]
    Init(InstallArgs),
    #[command(
        about = "将 YSS 接入已有普通项目",
        after_help = "示例：\n  yss attach --root ./existing-project --profile backend --dry-run\n\n已接入 YSS 的项目使用 yss sync。"
    )]
    Attach(InstallArgs),
    #[command(
        about = "同步已接入项目的内嵌模板与技能",
        after_help = "示例：\n  yss sync --root ./my-project --dry-run\n  yss sync --root ./my-project --installation skills --dry-run"
    )]
    Sync(SyncArgs),
    #[command(about = "查看或补装项目内的受管技能", subcommand)]
    Skills(SkillsCommand),
    #[command(about = "查看平台或生成 Java / Vue 工程", subcommand)]
    Scaffold(ScaffoldCommand),
    #[command(
        about = "检查项目或 CLI 安装及本地差异",
        after_help = "示例：\n  yss doctor --root ./my-project"
    )]
    Doctor(RootArgs),
    #[command(
        about = "恢复未完成事务或重试已提交事务的清理",
        after_help = "示例：\n  yss recover --root ./my-project --dry-run"
    )]
    Recover(RecoverArgs),
    #[command(
        about = "在线或离线安装、升级 Rust CLI",
        after_help = "示例（替换尖括号内的值）：\n  yss upgrade --to <版本> --tool-root <工具目录> --dry-run\n  yss upgrade --artifact <归档> --sha256 <摘要> --tool-root <工具目录> --dry-run\n\n程序升级与项目同步分别执行；在线预览仍会访问发行信息。"
    )]
    Upgrade(UpgradeArgs),
    #[command(about = "显示版本与构建来源")]
    Version,
}
impl Command {
    fn name(&self) -> &'static str {
        match self {
            Self::Workbench(args) => match &args.command {
                None => "workbench",
                Some(WorkbenchCommand::Snapshot) => "workbench.snapshot",
                Some(WorkbenchCommand::Query(_)) => "workbench.query",
                Some(WorkbenchCommand::Lifecycle { .. }) => "workbench.lifecycle",
            },
            Self::Init(_) => "init",
            Self::Attach(_) => "attach",
            Self::Sync(_) => "sync",
            Self::Skills(SkillsCommand::List(_)) => "skills list",
            Self::Skills(SkillsCommand::Ensure(_)) => "skills ensure",
            Self::Scaffold(ScaffoldCommand::Catalog) => "scaffold catalog",
            Self::Scaffold(ScaffoldCommand::Ddd(_)) => "scaffold ddd",
            Self::Scaffold(ScaffoldCommand::Mvc(_)) => "scaffold mvc",
            Self::Scaffold(ScaffoldCommand::Frontend(_)) => "scaffold frontend",
            Self::Doctor(_) => "doctor",
            Self::Recover(_) => "recover",
            Self::Upgrade(_) => "upgrade",
            Self::Version => "version",
        }
    }
}
#[derive(Args)]
pub struct WorkbenchArgs {
    #[arg(
        long,
        default_value_t = 0,
        help = "监听 127.0.0.1 的端口，0 由系统分配"
    )]
    pub port: u16,
    #[arg(long, help = "仅输出链接，不自动打开浏览器")]
    pub no_open: bool,
    #[arg(
        long,
        global = true,
        help = "独立个人数据目录（绝对路径，不能位于 Git 工作树或项目内）"
    )]
    pub data_dir: Option<PathBuf>,
    #[command(subcommand)]
    pub command: Option<WorkbenchCommand>,
}
#[derive(Subcommand)]
pub enum WorkbenchCommand {
    #[command(about = "只读输出个人状态与来源健康；可配合 --json")]
    Snapshot,
    #[command(about = "只读筛选已纳管的 Spec 或任务")]
    Query(WorkbenchQueryArgs),
    #[command(about = "只读查看 Spec 八阶段、任务与实际历史")]
    Lifecycle {
        #[arg(long)]
        spec: String,
    },
}
#[derive(Args)]
pub struct WorkbenchQueryArgs {
    #[arg(long, value_enum, default_value = "spec")]
    pub entity: crate::workbench::model::WorkEntity,
    #[arg(long, default_value = "")]
    pub q: String,
    #[arg(long = "project")]
    pub project_ids: Vec<String>,
    #[arg(long = "spec")]
    pub spec_ids: Vec<String>,
    #[arg(long = "stage", value_enum)]
    pub stages: Vec<crate::workbench::model::Stage>,
    #[arg(long = "status", value_enum)]
    pub statuses: Vec<crate::workbench::model::Status>,
    #[arg(long, value_enum, default_value = "active")]
    pub activity: crate::workbench::model::ActivityScope,
    #[arg(long)]
    pub include_closed: bool,
    #[arg(long = "source-problems")]
    pub source_problems_only: bool,
    #[arg(long, value_enum, default_value = "updatedDesc")]
    pub sort: crate::workbench::model::WorkSort,
}
#[derive(Args)]
pub struct Variables {
    #[arg(long, help = "模板中的项目名；新安装默认使用目录名")]
    pub project_name: Option<String>,
    #[arg(long, help = "模板中的业务领域；新安装默认为待补充")]
    pub business_domain: Option<String>,
    #[arg(long, help = "模板中的团队规模；新安装默认为待补充")]
    pub team_size: Option<String>,
}
#[derive(Args)]
pub struct InstallArgs {
    #[arg(long, help = "目标项目目录；init 要求新目录或空目录")]
    pub root: PathBuf,
    #[arg(
        long,
        value_enum,
        help = "项目类型：spec 业务规格与产品设计；backend 后端研发；frontend 前端研发"
    )]
    pub profile: Profile,
    #[arg(
        long,
        value_enum,
        help = "安装范围：project 含项目资料；skills 安装技能及必要资源（默认 project）"
    )]
    pub installation: Option<InstallationKind>,
    #[arg(long, help = "仅预览，不写入目标或保存计划")]
    pub dry_run: bool,
    #[command(flatten)]
    pub vars: Variables,
}
#[derive(Args)]
pub struct SyncArgs {
    #[arg(long, default_value = ".", help = "已接入的项目目录")]
    pub root: PathBuf,
    #[arg(long, value_enum, help = "校验已安装的项目类型；不能切换 profile")]
    pub profile: Option<Profile>,
    #[arg(long, value_enum, help = "切换安装范围；省略时沿用原范围")]
    pub installation: Option<InstallationKind>,
    #[arg(long, help = "仅预览，不写入目标或保存计划")]
    pub dry_run: bool,
    #[command(flatten)]
    pub vars: Variables,
}
#[derive(Args)]
pub struct RootArgs {
    #[arg(
        long,
        default_value = ".",
        help = "受管项目目录；doctor 也接受 CLI 工具目录"
    )]
    pub root: PathBuf,
    #[arg(long, value_enum, help = "校验项目类型；须与已安装 profile 一致")]
    pub profile: Option<Profile>,
}
#[derive(Args)]
pub struct RecoverArgs {
    #[arg(long, default_value = ".", help = "需要恢复的项目或 CLI 工具目录")]
    pub root: PathBuf,
    #[arg(long, help = "仅预览恢复，不修改文件")]
    pub dry_run: bool,
}
#[derive(Subcommand)]
pub enum SkillsCommand {
    #[command(
        about = "列出可用技能及其安装状态",
        after_help = "示例：\n  yss skills list --root ./my-project"
    )]
    List(RootArgs),
    #[command(
        about = "补装指定技能及其依赖资源",
        after_help = "示例：\n  yss skills ensure setup-yss-harness --root ./my-project --dry-run"
    )]
    Ensure(EnsureArgs),
}
#[derive(Args)]
pub struct EnsureArgs {
    #[command(flatten)]
    pub root: RootArgs,
    #[arg(long, help = "仅预览，不写入目标或保存计划")]
    pub dry_run: bool,
    #[arg(required=true,num_args=1..,help="一个或多个技能名称；可用名称由 skills list 查看")]
    pub skills: Vec<String>,
}
#[derive(Subcommand)]
pub enum ScaffoldCommand {
    #[command(
        about = "生成最小 Vue 3 前端工程；不安装依赖",
        after_help = "示例：\n  yss scaffold frontend --project-name my-frontend --output-dir . --dry-run\n\n输出父目录必须已存在，目标工程必须不存在。去掉 --dry-run 生成源码；运行需另行 pnpm install。"
    )]
    Frontend(FrontendScaffoldArgs),
    #[command(about = "查看可选平台、版本与配方")]
    Catalog,
    #[command(
        about = "生成 DDD 分层 Java 空工程",
        after_help = "示例（按 catalog 和项目环境确认版本与坐标）：\n  yss scaffold ddd --project-name sample-app --base-package com.example.sample --output-dir . --platform-profile spring-boot-3.5-jdk17 --spring-boot-version 3.5.16 --java-version 17 --group-id com.example --project-version 1.0.0-SNAPSHOT --parent-group-id com.yss.cloud --parent-artifact-id yss-cloud-microservice --parent-version 3.2.0-SNAPSHOT --yss-components-version 3.2.0-SNAPSHOT --dry-run\n\n先运行 yss scaffold catalog 查看平台与版本；输出目录下不能已有同名工程。"
    )]
    Ddd(ScaffoldArgs),
    #[command(
        about = "生成 MVC 分层 Java 空工程",
        after_help = "示例（按 catalog 和项目环境确认版本与坐标）：\n  yss scaffold mvc --project-name sample-app --base-package com.example.sample --output-dir . --platform-profile spring-boot-3.5-jdk17 --spring-boot-version 3.5.16 --java-version 17 --group-id com.example --project-version 1.0.0-SNAPSHOT --parent-group-id com.yss.cloud --parent-artifact-id yss-cloud-microservice --parent-version 3.2.0-SNAPSHOT --yss-components-version 3.2.0-SNAPSHOT --dry-run\n\n先运行 yss scaffold catalog 查看平台与版本；输出目录下不能已有同名工程。"
    )]
    Mvc(ScaffoldArgs),
}
#[derive(Args)]
pub struct FrontendScaffoldArgs {
    #[arg(long, help = "工程名称，使用小写 kebab-case")]
    pub project_name: String,
    #[arg(long, help = "已存在的输出父目录，预览也必须存在")]
    pub output_dir: PathBuf,
    #[arg(long, help = "仅预览，不创建工程或保存计划")]
    pub dry_run: bool,
}
#[derive(Args)]
pub struct ScaffoldArgs {
    #[arg(long, help = "工程名称，使用小写 kebab-case")]
    pub project_name: String,
    #[arg(long, help = "Java 基础包名，例如 com.example.demo")]
    pub base_package: String,
    #[arg(long, help = "输出父目录；实际生成时须已存在")]
    pub output_dir: PathBuf,
    #[arg(long, help = "平台标识，使用 scaffold catalog 查看")]
    pub platform_profile: String,
    #[arg(long, help = "与平台匹配的精确 Spring Boot 版本")]
    pub spring_boot_version: String,
    #[arg(long, help = "与平台匹配的 Java 主版本，例如 17")]
    pub java_version: String,
    #[arg(long, help = "生成工程的 Maven groupId")]
    pub group_id: String,
    #[arg(long, help = "生成工程的 Maven 版本")]
    pub project_version: String,
    #[arg(long, help = "父 POM 的 Maven groupId")]
    pub parent_group_id: String,
    #[arg(long, help = "父 POM 的 Maven artifactId")]
    pub parent_artifact_id: String,
    #[arg(long, help = "父 POM 的精确版本")]
    pub parent_version: String,
    #[arg(long, help = "YSS 组件 BOM 的精确版本")]
    pub yss_components_version: String,
    #[arg(long, help = "仅预览，不创建工程或保存计划")]
    pub dry_run: bool,
}
#[derive(Args)]
pub struct UpgradeArgs {
    #[arg(
        long,
        value_enum,
        conflicts_with = "artifact",
        help = "在线发行来源；默认 github，不能与 --artifact 同用"
    )]
    pub source: Option<crate::network::ReleaseSource>,
    #[arg(
        long,
        help = "指定正式版本；在线省略时查询 latest 并校验正式版，离线指定时须与归档一致"
    )]
    pub to: Option<String>,
    #[arg(
        long,
        help = "CLI 工具目录；省略时须有 Rust 安装收据，初装须显式指定新目录或空目录"
    )]
    pub tool_root: Option<PathBuf>,
    #[arg(long, requires = "sha256", help = "离线发行归档；须同时提供 --sha256")]
    pub artifact: Option<PathBuf>,
    #[arg(
        long,
        requires = "artifact",
        help = "离线归档的 64 位小写 SHA-256；须同时提供 --artifact"
    )]
    pub sha256: Option<String>,
    #[arg(long, help = "仅预览，不安装；在线预览仍访问发行信息")]
    pub dry_run: bool,
}

#[derive(Serialize)]
#[serde(untagged)]
enum Report<'a> {
    Workbench(crate::workbench::model::Snapshot),
    WorkbenchQuery(crate::workbench::model::QueryResult),
    WorkbenchLifecycle(crate::workbench::model::LifecycleView),
    Help { help: String },
    Version(&'a BuildInfo),
    Catalog(crate::scaffold::CatalogReport),
    Scaffold(crate::scaffold::ScaffoldReport),
    FrontendScaffold(crate::scaffold::frontend::FrontendReport),
    Upgrade(Box<crate::updater::UpgradeReport>),
    ToolDoctor(Box<crate::updater::ToolDoctorReport>),
    Project(Box<crate::project::ProjectReport>),
    Skills(crate::project::SkillsReport),
    Doctor(Box<crate::project::DoctorReport>),
    Transaction(crate::transaction::TransactionReport),
}

fn dispatch<'a>(
    command: Command,
    bundle: &[u8],
    build: &'a BuildInfo,
    cancel: &Cancellation,
) -> Result<Report<'a>> {
    cancel.check()?;
    match command {
        Command::Workbench(args) => {
            let path = crate::workbench::store::resolve_data_dir(args.data_dir.as_deref())?;
            let snapshot = crate::workbench::service::snapshot(
                &crate::workbench::store::read_state(&path)?,
                cancel,
            )?;
            match args.command {
                Some(WorkbenchCommand::Query(q)) => {
                    Ok(Report::WorkbenchQuery(crate::workbench::query::query(
                        &snapshot,
                        &crate::workbench::model::WorkQuery {
                            entity: q.entity,
                            q: q.q,
                            project_ids: q.project_ids,
                            spec_ids: q.spec_ids,
                            stages: q.stages,
                            statuses: q.statuses,
                            activity: q.activity,
                            include_closed: q.include_closed,
                            source_problems_only: q.source_problems_only,
                            sort: q.sort,
                        },
                    )?))
                }
                Some(WorkbenchCommand::Lifecycle { spec }) => Ok(Report::WorkbenchLifecycle(
                    crate::workbench::lifecycle::build_lifecycle(&snapshot, &spec)?,
                )),
                _ => Ok(Report::Workbench(snapshot)),
            }
        }
        Command::Version => Ok(Report::Version(build)),
        Command::Scaffold(ScaffoldCommand::Catalog) => Ok(Report::Catalog(
            crate::scaffold::catalog(&crate::bundle::load(bundle)?)?,
        )),
        Command::Scaffold(ScaffoldCommand::Frontend(args)) => Ok(Report::FrontendScaffold(
            crate::scaffold::frontend::generate(
                &crate::bundle::load(bundle)?,
                &crate::scaffold::frontend::FrontendRequest {
                    input: crate::scaffold::frontend::FrontendInputs {
                        project_name: args.project_name,
                    },
                    output_dir: args.output_dir,
                    dry_run: args.dry_run,
                },
                cancel,
            )?,
        )),
        Command::Scaffold(ScaffoldCommand::Ddd(args)) => {
            scaffold_report(args, crate::scaffold::Architecture::Ddd, bundle, cancel)
        }
        Command::Scaffold(ScaffoldCommand::Mvc(args)) => {
            scaffold_report(args, crate::scaffold::Architecture::Mvc, bundle, cancel)
        }
        Command::Init(args) => project_report(
            crate::project::ProjectRequest::Init(install_request(args)),
            bundle,
            cancel,
        ),
        Command::Attach(args) => project_report(
            crate::project::ProjectRequest::Attach(install_request(args)),
            bundle,
            cancel,
        ),
        Command::Sync(args) => project_report(
            crate::project::ProjectRequest::Sync(crate::project::SyncRequest {
                root: args.root,
                profile: args.profile,
                installation: args.installation,
                variables: variables(args.vars),
                dry_run: args.dry_run,
            }),
            bundle,
            cancel,
        ),
        Command::Skills(SkillsCommand::Ensure(args)) => project_report(
            crate::project::ProjectRequest::Ensure(crate::project::EnsureRequest {
                root: args.root.root,
                profile: args.root.profile,
                skills: args.skills.into_iter().collect(),
                dry_run: args.dry_run,
            }),
            bundle,
            cancel,
        ),
        Command::Skills(SkillsCommand::List(args)) => {
            let report = crate::project::list_skills(&args.root, &crate::bundle::load(bundle)?)?;
            if args.profile.is_some_and(|p| p != report.profile) {
                return Err(YssError::new(
                    ErrorCode::Conflict,
                    "所选 profile 与安装身份不同",
                ));
            }
            Ok(Report::Skills(report))
        }
        Command::Doctor(args) => {
            if crate::updater::is_tool(&args.root)? {
                return Ok(Report::ToolDoctor(Box::new(crate::updater::doctor(
                    &args.root,
                )?)));
            }
            let report = crate::project::doctor(&args.root, &crate::bundle::load(bundle)?)?;
            if let Some(state) = &report.installation
                && args.profile.is_some_and(|p| p != state.profile)
            {
                return Err(YssError::new(
                    ErrorCode::Conflict,
                    "所选 profile 与安装身份不同",
                ));
            }
            Ok(Report::Doctor(Box::new(report)))
        }
        Command::Recover(args) => Ok(Report::Transaction(crate::transaction::recover(
            &args.root,
            args.dry_run,
            cancel,
        )?)),
        Command::Upgrade(args) => {
            let request = crate::updater::UpgradeRequest {
                source: args.source,
                to: args.to,
                tool_root: args.tool_root,
                artifact: args.artifact,
                sha256: args.sha256.map(TryInto::try_into).transpose()?,
                dry_run: args.dry_run,
            };
            Ok(Report::Upgrade(Box::new(crate::updater::execute(
                crate::updater::prepare(&request, build, cancel)?,
                cancel,
            )?)))
        }
    }
}

fn scaffold_report<'a>(
    args: ScaffoldArgs,
    architecture: crate::scaffold::Architecture,
    bundle: &[u8],
    cancel: &Cancellation,
) -> Result<Report<'a>> {
    use crate::scaffold::{ScaffoldInputs, ScaffoldRequest};
    let request = ScaffoldRequest {
        architecture,
        output_dir: args.output_dir,
        dry_run: args.dry_run,
        input: ScaffoldInputs {
            project_name: args.project_name,
            base_package: args.base_package,
            platform_profile: args.platform_profile,
            spring_boot_version: args.spring_boot_version,
            java_version: args
                .java_version
                .parse()
                .map_err(|_| YssError::new(ErrorCode::Argument, "Java 版本必须为整数"))?,
            group_id: args.group_id,
            project_version: args.project_version,
            parent_group_id: args.parent_group_id,
            parent_artifact_id: args.parent_artifact_id,
            parent_version: args.parent_version,
            yss_components_version: args.yss_components_version,
        },
    };
    Ok(Report::Scaffold(crate::scaffold::generate(
        &crate::bundle::load(bundle)?,
        &request,
        cancel,
    )?))
}
fn variables(args: Variables) -> std::collections::BTreeMap<String, String> {
    [
        ("projectName", args.project_name),
        ("businessDomain", args.business_domain),
        ("teamSize", args.team_size),
    ]
    .into_iter()
    .filter_map(|(key, value)| value.map(|v| (key.into(), v)))
    .collect()
}
fn install_request(args: InstallArgs) -> crate::project::InstallRequest {
    crate::project::InstallRequest {
        root: args.root,
        profile: args.profile,
        installation: args.installation,
        variables: variables(args.vars),
        dry_run: args.dry_run,
    }
}
fn project_report<'a>(
    request: crate::project::ProjectRequest,
    bundle: &[u8],
    cancel: &Cancellation,
) -> Result<Report<'a>> {
    let bundle = crate::bundle::load(bundle)?;
    let plan = crate::project::plan(&bundle, &request)?;
    Ok(Report::Project(Box::new(crate::project::execute(
        plan, cancel,
    )?)))
}

pub fn run(
    mut args: Vec<OsString>,
    bundle_bytes: &[u8],
    build: &BuildInfo,
    cancel: &Cancellation,
    out: &mut dyn Write,
    err: &mut dyn Write,
    assets: crate::workbench::server::AssetLoader,
) -> i32 {
    let json = args
        .iter()
        .take_while(|s| *s != "--")
        .fold(false, |value, arg| {
            if arg == "--json" || arg == "--json=true" {
                true
            } else if arg == "--json=false" {
                false
            } else {
                value
            }
        });
    // clap's synthetic help subcommand does not inherit global arguments.
    let boundary = args.iter().position(|a| a == "--").unwrap_or(args.len());
    if let Some(help) = args[..boundary].iter().position(|a| a == "help")
        && args[..help]
            .iter()
            .all(|a| matches!(a.to_str(), Some("--json" | "--json=true" | "--json=false")))
    {
        args.retain(|a| !matches!(a.to_str(), Some("--json" | "--json=true" | "--json=false")));
    }
    let parsed = Cli::try_parse_from(std::iter::once(OsString::from("yss")).chain(args));
    let (name, result) = match parsed {
        Ok(cli) if cli.version => ("version", Ok(Report::Version(build))),
        Ok(cli) => match cli.command {
            Some(command) => {
                let name = command.name();
                if let Command::Workbench(ref args) = command
                    && args.command.is_none()
                {
                    let result = if json {
                        Err(YssError::new(
                            ErrorCode::Argument,
                            "持续服务不支持 --json；请使用 workbench snapshot --json",
                        ))
                    } else {
                        crate::workbench::store::resolve_data_dir(args.data_dir.as_deref())
                            .and_then(|data_dir| {
                                crate::workbench::server::serve(
                                    crate::workbench::server::ServeOptions {
                                        data_dir,
                                        port: args.port,
                                        open_browser: !args.no_open,
                                    },
                                    assets,
                                    cancel,
                                    err,
                                )
                            })
                    };
                    (
                        name,
                        result.map(|()| Report::Help {
                            help: String::new(),
                        }),
                    )
                } else {
                    (name, dispatch(command, bundle_bytes, build, cancel))
                }
            }
            None => (
                "help",
                Ok(Report::Help {
                    help: Cli::command().render_long_help().to_string(),
                }),
            ),
        },
        Err(e) if e.kind() == clap::error::ErrorKind::DisplayHelp => (
            "help",
            Ok(Report::Help {
                help: e.to_string(),
            }),
        ),
        Err(_) => (
            "arguments",
            Err(YssError::new(
                ErrorCode::Argument,
                "参数不正确；请运行 yss --help 查看用法",
            )),
        ),
    };
    let exit = result.as_ref().err().map_or(0, YssError::exit_code);
    let written = if json {
        match result {
            Ok(report) => output::write(out, &OutputEnvelope::ok(name, report)),
            Err(ref error) => output::write(out, &OutputEnvelope::<()>::error(name, error)),
        }
    } else {
        match result {
            Ok(Report::Help { help }) => out.write_all(help.as_bytes()).map_err(Into::into),
            Ok(report) => serde_json::to_writer_pretty(&mut *out, &report)
                .map_err(|_| YssError::new(ErrorCode::Io, "无法写出结果"))
                .and_then(|()| out.write_all(b"\n").map_err(Into::into)),
            Err(error) => writeln!(
                err,
                "{}{}",
                error.message,
                error.path.map(|p| format!("：{p}")).unwrap_or_default()
            )
            .map_err(Into::into),
        }
    };
    if written.is_err() {
        let _ = writeln!(err, "无法写出结果");
        1
    } else {
        exit
    }
}
