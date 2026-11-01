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
    about = "YSS 项目、技能与 Java 工程工具",
    disable_version_flag = true
)]
pub struct Cli {
    #[arg(long,global=true,action=clap::ArgAction::Set,num_args=0..=1,default_missing_value="true",default_value="false",require_equals=true)]
    pub json: bool,
    #[arg(short = 'V', long, global = true)]
    pub version: bool,
    #[command(subcommand)]
    pub command: Option<Command>,
}
#[derive(Subcommand)]
pub enum Command {
    #[command(about = "初始化空项目目录")]
    Init(InstallArgs),
    #[command(about = "将 YSS 安装到已有项目")]
    Attach(InstallArgs),
    #[command(about = "同步已有 Rust 安装")]
    Sync(SyncArgs),
    #[command(about = "查看或安装技能", subcommand)]
    Skills(SkillsCommand),
    #[command(about = "生成业务空白 Java 工程", subcommand)]
    Scaffold(ScaffoldCommand),
    #[command(about = "检查安装与本地差异")]
    Doctor(RootArgs),
    #[command(about = "恢复未完成事务")]
    Recover(RecoverArgs),
    #[command(about = "安装或升级 Rust CLI")]
    Upgrade(UpgradeArgs),
    #[command(about = "显示版本与构建来源")]
    Version,
}
impl Command {
    fn name(&self) -> &'static str {
        match self {
            Self::Init(_) => "init",
            Self::Attach(_) => "attach",
            Self::Sync(_) => "sync",
            Self::Skills(SkillsCommand::List(_)) => "skills list",
            Self::Skills(SkillsCommand::Ensure(_)) => "skills ensure",
            Self::Scaffold(ScaffoldCommand::Catalog) => "scaffold catalog",
            Self::Scaffold(ScaffoldCommand::Ddd(_)) => "scaffold ddd",
            Self::Scaffold(ScaffoldCommand::Mvc(_)) => "scaffold mvc",
            Self::Doctor(_) => "doctor",
            Self::Recover(_) => "recover",
            Self::Upgrade(_) => "upgrade",
            Self::Version => "version",
        }
    }
}
#[derive(Args)]
pub struct Variables {
    #[arg(long)]
    pub project_name: Option<String>,
    #[arg(long)]
    pub business_domain: Option<String>,
    #[arg(long)]
    pub team_size: Option<String>,
}
#[derive(Args)]
pub struct InstallArgs {
    #[arg(long)]
    pub root: PathBuf,
    #[arg(long, value_enum)]
    pub profile: Profile,
    #[arg(long, value_enum)]
    pub installation: Option<InstallationKind>,
    #[arg(long)]
    pub dry_run: bool,
    #[command(flatten)]
    pub vars: Variables,
}
#[derive(Args)]
pub struct SyncArgs {
    #[arg(long, default_value = ".")]
    pub root: PathBuf,
    #[arg(long, value_enum)]
    pub profile: Option<Profile>,
    #[arg(long, value_enum)]
    pub installation: Option<InstallationKind>,
    #[arg(long)]
    pub dry_run: bool,
    #[command(flatten)]
    pub vars: Variables,
}
#[derive(Args)]
pub struct RootArgs {
    #[arg(long, default_value = ".")]
    pub root: PathBuf,
    #[arg(long, value_enum)]
    pub profile: Option<Profile>,
}
#[derive(Args)]
pub struct RecoverArgs {
    #[arg(long, default_value = ".")]
    pub root: PathBuf,
    #[arg(long)]
    pub dry_run: bool,
}
#[derive(Subcommand)]
pub enum SkillsCommand {
    List(RootArgs),
    Ensure(EnsureArgs),
}
#[derive(Args)]
pub struct EnsureArgs {
    #[command(flatten)]
    pub root: RootArgs,
    #[arg(long)]
    pub dry_run: bool,
    #[arg(required=true,num_args=1..)]
    pub skills: Vec<String>,
}
#[derive(Subcommand)]
pub enum ScaffoldCommand {
    Catalog,
    Ddd(ScaffoldArgs),
    Mvc(ScaffoldArgs),
}
#[derive(Args)]
pub struct ScaffoldArgs {
    #[arg(long)]
    pub project_name: String,
    #[arg(long)]
    pub base_package: String,
    #[arg(long)]
    pub output_dir: PathBuf,
    #[arg(long)]
    pub platform_profile: String,
    #[arg(long)]
    pub spring_boot_version: String,
    #[arg(long)]
    pub java_version: String,
    #[arg(long)]
    pub group_id: String,
    #[arg(long)]
    pub project_version: String,
    #[arg(long)]
    pub parent_group_id: String,
    #[arg(long)]
    pub parent_artifact_id: String,
    #[arg(long)]
    pub parent_version: String,
    #[arg(long)]
    pub yss_components_version: String,
    #[arg(long)]
    pub dry_run: bool,
}
#[derive(Args)]
pub struct UpgradeArgs {
    #[arg(long)]
    pub to: Option<String>,
    #[arg(long)]
    pub tool_root: Option<PathBuf>,
    #[arg(long, requires = "sha256")]
    pub artifact: Option<PathBuf>,
    #[arg(long, requires = "artifact")]
    pub sha256: Option<String>,
    #[arg(long)]
    pub dry_run: bool,
}

#[derive(Serialize)]
#[serde(untagged)]
enum Report<'a> {
    Help { help: String },
    Version(&'a BuildInfo),
    Catalog(crate::scaffold::CatalogReport),
    Scaffold(crate::scaffold::ScaffoldReport),
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
        Command::Version => Ok(Report::Version(build)),
        Command::Scaffold(ScaffoldCommand::Catalog) => Ok(Report::Catalog(
            crate::scaffold::catalog(&crate::bundle::load(bundle)?)?,
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
                (name, dispatch(command, bundle_bytes, build, cancel))
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
