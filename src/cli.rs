use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
    model::{BuildInfo, InstallationKind, Profile},
    output::{self, OutputEnvelope},
    workbench::{
        model::{Action, Stage},
        progress::{ConfirmationDecision, ConfirmationInput},
        status::ReportKind,
    },
};
use clap::{
    Args, CommandFactory, FromArgMatches, Parser, Subcommand,
    error::{ContextKind, ContextValue, ErrorKind},
};
use serde::Serialize;
use std::{
    ffi::OsString,
    io::{Read, Write},
    path::PathBuf,
};

#[derive(Parser)]
#[command(
    name = "yss",
    about = "管理 YSS 项目资源、生成 Java / Vue 工程并运行本机 Spec 工作台",
    after_help = "常用命令\n  yss version --json\n  yss init --root ./my-project --profile spec --dry-run\n  yss skills list --root ./my-project\n  yss workbench spec-status --help\n  yss help init\n\n子命令帮助列出 --dry-run 时可用该参数预览，去掉该参数执行写入。完整工程接入由 Agent 按 setup-yss-harness 的 references/project-onboarding.md 核实 Git、远程和 Issue tracker，并按需配置 Home 关系。",
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
    #[command(
        about = "启动个人本地 Spec 工作台，或查询、登记和续办 Spec",
        after_help = WORKBENCH_HELP
    )]
    Workbench(WorkbenchArgs),
    #[command(
        about = "在新目录或空目录安装 YSS 项目资源",
        after_help = "示例\n  yss init --root ./my-project --profile spec --dry-run\n\n--root 指向空目录或待创建的目录，新目录的父目录须已存在。去掉 --dry-run 执行安装。\n已有内容的目录使用 yss attach，已接入 YSS 的项目使用 yss sync。\n完整工程接入由 Agent 按 setup-yss-harness 的 references/project-onboarding.md 核实真实 Git 边界、远程和 Issue tracker，并按需配置 Home。已知远程已有内容时，先复用 checkout 或 clone。"
    )]
    Init(InstallArgs),
    #[command(
        about = "将 YSS 项目资源安装到已有目录",
        after_help = "示例\n  yss attach --root ./existing-project --profile backend --installation skills --dry-run\n\n--root 指向已有且尚未接入 YSS 的项目根目录。去掉 --dry-run 执行安装。已接入 YSS 的项目使用 yss sync。\n完整工程接入由 Agent 按 setup-yss-harness 的 references/project-onboarding.md 核实真实 Git 边界、复用所属仓、核实远程和 Issue tracker，并按需补齐 Home 关系。保留已有 tracker。新工程尚无已确认 tracker 且已核实唯一 GitHub/GitLab 项目时，可采用该项目 Issues 并明确报告。"
    )]
    Attach(InstallArgs),
    #[command(
        about = "将当前 CLI 内嵌的模板与技能同步到已接入项目",
        after_help = "示例\n  yss sync --root ./my-project --dry-run --json\n  yss sync --root ./my-project --installation skills --dry-run --json\n\n预览后查看 result.changes 和 result.preserved，再去掉 --dry-run 执行。\n安装范围或内嵌模板变化时，已移出安装范围且与原收据一致的受管文件可能被删除。用户维护的文件予以保留，本地内容与模板变更冲突时停止并报告路径。"
    )]
    Sync(SyncArgs),
    #[command(about = "查看或补装项目内的受管技能", subcommand)]
    Skills(SkillsCommand),
    #[command(about = "查看平台或生成 Java / Vue 工程", subcommand)]
    Scaffold(ScaffoldCommand),
    #[command(
        about = "只读检查项目或 CLI 的安装、本地差异与事务状态",
        after_help = "示例\n  yss doctor --root ./my-project --json\n  yss doctor --root ./yss-tools --json\n\n--root 指向项目根目录或 CLI 工具目录，省略时检查当前目录。--profile 用于校验已有项目安装的类型。\n查看 result.installation 的安装记录、result.differences 的本地文件差异，以及 result.transactions.pending 的未完成事务。待清理的事务列在 result.transactions.cleanupPending。\n项目的 result.installation 为 null 表示没有项目安装收据。使用 yss sync --dry-run 预览当前 CLI 内嵌资源对项目的更新。"
    )]
    Doctor(RootArgs),
    #[command(
        about = "恢复未完成事务或重试已提交事务的清理",
        after_help = "示例\n  yss doctor --root ./my-project --json\n  yss recover --root ./my-project --dry-run\n  yss recover --root ./my-project\n\n有未完成事务时，--dry-run 检查回滚条件并预览操作数，去掉该参数执行回滚。\n没有未完成事务且仍有待清理材料时，实际运行会重试清理。已提交的文件变更继续生效。"
    )]
    Recover(RecoverArgs),
    #[command(
        about = "在线或离线安装、升级 Rust CLI",
        after_help = "示例（替换尖括号内的值）：\n  yss upgrade --to <版本> --tool-root <工具目录> --dry-run\n  yss upgrade --artifact <归档> --sha256 <摘要> --tool-root <工具目录> --dry-run\n\n程序升级与项目同步分别执行；在线预览仍会访问发行信息。仅安装或升级 CLI 不初始化工程 Git、远程或 Issue tracker；完整工程接入参见 setup-yss-harness 的 references/project-onboarding.md。"
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
                Some(WorkbenchCommand::SpecStatus { .. }) => "workbench.spec-status",
                Some(WorkbenchCommand::LifecycleStatus { .. }) => "workbench.lifecycle-status",
                Some(WorkbenchCommand::LifecycleConfirm(_)) => "workbench.lifecycle-confirm",
                Some(WorkbenchCommand::LifecycleAutoConfirm { .. }) => {
                    "workbench.lifecycle-auto-confirm"
                }
                Some(WorkbenchCommand::RecommendStatus { .. }) => "workbench.recommend-status",
                Some(WorkbenchCommand::Lifecycle { .. }) => "workbench.lifecycle",
                Some(WorkbenchCommand::Register { .. }) => "workbench.register",
                Some(WorkbenchCommand::Request { .. }) => "workbench.request",
                Some(WorkbenchCommand::Respond(_)) => "workbench.respond",
                Some(WorkbenchCommand::ReviewMaterials(_)) => "workbench.review-materials",
                Some(WorkbenchCommand::Progress(command)) => match command {
                    WorkbenchProgressCommand::SetGoal(_) => "workbench.progress.set-goal",
                    WorkbenchProgressCommand::Report(_) => "workbench.progress.report",
                    WorkbenchProgressCommand::Confirm(_) => "workbench.progress.confirm",
                    WorkbenchProgressCommand::Reconcile { .. } => "workbench.progress.reconcile",
                    WorkbenchProgressCommand::ObserveInputs { .. } => {
                        "workbench.progress.observe-inputs"
                    }
                },
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
    #[command(
        about = "登记目标、上报实际成果并核实生命周期进度",
        subcommand,
        after_help = WORKBENCH_PROGRESS_HELP
    )]
    Progress(WorkbenchProgressCommand),
    #[command(
        about = "登记或刷新一个主 Spec 及关联产物，保留个人备注与历史",
        after_help = WORKBENCH_REGISTER_HELP
    )]
    Register {
        #[arg(long, help = "主 Spec 所在工程根目录")]
        root: PathBuf,
        #[arg(long, help = "主 Spec 相对工程根的路径")]
        source: String,
    },
    #[command(
        about = "只读生成包含问题答复的 Agent 续办要求",
        after_help = WORKBENCH_REQUEST_HELP
    )]
    Request {
        #[arg(long, help = "已登记的实际 Spec ID")]
        spec_id: String,
    },
    #[command(about = "保存用户已明确给出的答复或延期；答复不等于解决")]
    Respond(WorkbenchResponseArgs),
    #[command(
        about = "核实一个 Spec 的材料，记录参考、依赖或采用版本",
        after_help = REVIEW_MATERIALS_HELP
    )]
    ReviewMaterials(ProgressInputArgs),
    #[command(
        about = "只读输出个人状态与来源健康",
        after_help = WORKBENCH_SNAPSHOT_HELP
    )]
    Snapshot,
    #[command(
        about = "只读筛选已纳管的 Spec 或任务",
        after_help = WORKBENCH_QUERY_HELP
    )]
    Query(WorkbenchQueryArgs),
    #[command(
        about = "只读核实已登记 Spec 的来源、内容、材料与有效进度",
        after_help = WORKBENCH_SPEC_STATUS_HELP
    )]
    SpecStatus {
        #[arg(long, help = "已登记的实际 Spec ID")]
        spec_id: String,
    },
    #[command(
        about = "只读核实 Spec 生命周期阶段、证据、目标和确认状态",
        after_help = WORKBENCH_LIFECYCLE_STATUS_HELP
    )]
    LifecycleStatus {
        #[arg(long, help = "已登记的实际 Spec ID")]
        spec_id: String,
    },
    #[command(
        about = "记录用户对 Spec 生命周期当前范围和证据的明确决定",
        after_help = WORKBENCH_LIFECYCLE_CONFIRM_HELP
    )]
    LifecycleConfirm(LifecycleConfirmArgs),
    #[command(
        about = "核实当前证据并自动记录适用的生命周期确认",
        after_help = WORKBENCH_LIFECYCLE_AUTO_CONFIRM_HELP
    )]
    LifecycleAutoConfirm {
        #[arg(long, help = "已登记的实际 Spec ID")]
        spec_id: String,
        #[arg(long, help = "稳定请求 ID，重试时沿用同一 ID")]
        request_id: String,
    },
    #[command(
        about = "自动根据当前证据推荐 Spec 生命周期状态及下一步",
        after_help = WORKBENCH_RECOMMEND_STATUS_HELP
    )]
    RecommendStatus {
        #[arg(long, help = "已登记的实际 Spec ID")]
        spec_id: String,
    },
    #[command(
        about = "只读查看 Spec 八阶段、任务与实际历史",
        after_help = WORKBENCH_LIFECYCLE_HELP
    )]
    Lifecycle {
        #[arg(long, help = "已登记的实际 Spec ID")]
        spec: String,
    },
}

#[derive(Subcommand)]
pub enum WorkbenchProgressCommand {
    #[command(
        about = "只读采集检查采用的文件和目录摘要",
        after_help = PROGRESS_OBSERVE_INPUTS_HELP
    )]
    ObserveInputs {
        #[arg(long, help = "已登记的实际工程 ID")]
        project: String,
        #[arg(
            long = "path",
            required = true,
            help = "工程相对文件或目录路径，可重复指定"
        )]
        paths: Vec<String>,
    },
    #[command(
        about = "登记本次目标、适用阶段、参与工程和规格版本",
        after_help = PROGRESS_SET_GOAL_HELP
    )]
    SetGoal(ProgressInputArgs),
    #[command(
        about = "上报带版本与证据的阶段成果，重复上报使用原 ID",
        after_help = PROGRESS_REPORT_HELP
    )]
    Report(ProgressInputArgs),
    #[command(
        about = "记录用户对当前范围、版本和证据的明确决定",
        after_help = PROGRESS_CONFIRM_HELP
    )]
    Confirm(ProgressInputArgs),
    #[command(
        about = "复核已登记 Spec 的当前事实，历史时间缺失时保留未知",
        after_help = PROGRESS_RECONCILE_HELP
    )]
    Reconcile {
        #[arg(long, help = "已登记的实际 Spec ID")]
        spec_id: String,
        #[arg(long, help = "稳定请求 ID，重试时沿用同一 ID")]
        request_id: String,
    },
}

const WORKBENCH_HELP: &str = r#"启动本机浏览器工作台
  yss workbench
  yss workbench --no-open

只读定位、查看或续办已登记的 Spec
  yss workbench snapshot --json
  yss workbench query --entity spec --q "<标题或主文件路径关键词>" --json
  yss workbench spec-status --spec-id "<实际 Spec ID>" --json
  yss workbench lifecycle-status --spec-id "<实际 Spec ID>" --json
  yss workbench recommend-status --spec-id "<实际 Spec ID>" --json
  yss workbench lifecycle --spec "<实际 Spec ID>" --json
  yss workbench request --spec-id "<实际 Spec ID>" --json

目标或进度写入时核实并自动确认有效证据。主动触发可用 yss workbench lifecycle-auto-confirm --help。
需要记录用户明确决定时，可用 yss workbench lifecycle-confirm --help。
启动持续服务时不能使用 --json。读取数据时使用对应子命令。
如原工作台使用自定义数据目录，每条命令都在 workbench 后加原 --data-dir "<绝对路径>"。
续办时核对查询结果对应的工程根与主文件，沿用已登记的 Spec ID。"#;

const WORKBENCH_REGISTER_HELP: &str = r#"以实际工程根及主 Spec 路径登记或刷新
  yss workbench register --root "/absolute/project" --source "docs/spec/topic/spec.md" --json

替换示例路径。--source 相对 --root，沿用同一主文件刷新已有登记。
保存 result.projectId 和 result.specId 供后续操作使用。result.revision 为返回的工作台修订号。
使用自定义目录时，在 workbench 后加原 --data-dir。"#;

const WORKBENCH_REQUEST_HELP: &str = r#"生成已登记 Spec 的续办要求
  yss workbench request --spec-id "<实际 Spec ID>" --json

--json 在 result 中返回续办对象，result.text 可复制给接手者，
result.questions 和 result.history 可分别读取当前问题及历史答复。
result.projectRoot 和 result.sourcePath 标明工程根及相对主文件路径。
省略 --json 时直接输出格式化的续办对象，续办文本在顶层 text。
沿用原 --data-dir，并通过 lifecycle 查看当前目标版本和采用的 Spec 摘要。"#;

const WORKBENCH_SNAPSHOT_HELP: &str = r#"  yss workbench snapshot --json

在 result.projects 和 result.specs 中查看工程与 Spec。各项的 record.id 为登记 ID，
Spec 的 record.sourcePath 为工程相对主文件路径。使用自定义目录时，在 workbench 后加原 --data-dir。"#;

const WORKBENCH_QUERY_HELP: &str = r#"按标题或主文件路径定位现有 Spec，包含停用和已结束项
  yss workbench query --entity spec --q "<标题或路径关键词>" --activity all --include-closed --json

查询有效进度待复核的 Spec
  yss workbench query --entity spec --progress-basis effective --attention needsReview --json

默认查询启用的 Spec，隐藏已完成和已取消项，按最近更新排序。
personal 使用个人标记。effective 用证据和当前版本的人工更正汇总 Spec 进度，
缺少有效进度汇总时沿用个人标记。--attention 筛选进度汇总中的关注项。
在 result.items 查看命中条目，specId 为所属 Spec ID，projectId 为所属工程 ID，
id 为当前 Spec 或任务 ID。使用自定义目录时，在 workbench 后加原 --data-dir。"#;

const WORKBENCH_SPEC_STATUS_HELP: &str = r#"核实一个已登记的 Spec
  yss workbench spec-status --spec-id "<实际 Spec ID>"
  yss workbench --data-dir "/absolute/personal-data" spec-status --spec-id "<实际 Spec ID>" --json

未知 ID 可先用 query --entity spec --q "<标题或路径关键词>" --activity all --include-closed --json 定位。
核对 projectRoot 和 sourcePath，并沿用原工作台的 --data-dir。

查看 sourceHealth、content.status、content.questions、content.blockers、content.materialChanges 及 progress。
content.artifactDigest 是当前采用的产物摘要，content.observedArtifactDigest 是登记工程当前可观察的产物摘要，
content.confirmedVersion 是已有内容确认，progress.goalSpecDigest 是已登记目标采用的摘要。
来源缺失会在结果中显示，未登记的 Spec ID 会报错。此命令只读，旧数据保持原格式。
默认输出格式化 JSON，--json 将报告放在统一信封的 result。阶段的当前证据可用 lifecycle-status 查看。"#;

const WORKBENCH_LIFECYCLE_STATUS_HELP: &str = r#"只读核实当前生命周期
  yss workbench lifecycle-status --spec-id "<实际 Spec ID>" --json

result.progress.stages 各项包含 progress、validity、evidence 及适用的确认信息。
confirmation.origin 为 automatic 时，表示由当前证据自动核实。
进度与证据有效性分别判断。progress.autoConfirmableStages 列出当前可自动确认的阶段，无候选时可省略。
使用自定义目录时，在 workbench 后加原 --data-dir。完整任务与历史可用 lifecycle 查看。"#;

const WORKBENCH_LIFECYCLE_CONFIRM_HELP: &str = r#"需要记录用户明确决定时，先用 lifecycle-status --spec-id "<实际 Spec ID>" --json 核对当前范围及证据。
按用户实际核对并明确给出的决定填写参数。--scope 须与 progress.goal.scope 逐字相同。

  yss workbench lifecycle-confirm --spec-id "<实际 Spec ID>" --goal-version "<目标版本>" \
    --stage verification --scope "<用户核对的完整范围>" --evidence-digest "<证据摘要>" \
    --decision confirm --source "<实际确认人或答复出处>" --request-id "<稳定请求 ID>" --json

spec 对应规格，verification 对应业务验收，delivery 对应最终交付。
写入前复核当前版本、范围和证据，变化后须重新读取并核对用户决定。重试沿用同一请求 ID 和参数。
使用自定义目录时，在 workbench 后加原 --data-dir。保存的决定可用 lifecycle 查历史。"#;

const WORKBENCH_LIFECYCLE_AUTO_CONFIRM_HELP: &str = r#"核实当前证据，并自动记录符合条件的规格、验证及交付确认
  yss workbench lifecycle-auto-confirm --spec-id "<实际 Spec ID>" --request-id "<本次写入的稳定请求 ID>" --json

程序读取当前目标、范围、有效证据和已有决定。同一目标的明确退回优先于自动确认。
验证报告须包含通过凭证，并覆盖目标中登记的验收引用。没有验收引用时，可自动确认有范围依据的验证跳过。
证据缺失、失败或过期时保留待核实状态。
核实后的阶段与确认来源见 lifecycle-status。请求重试沿用原 --request-id；证据更新后重新触发用新 ID。
使用自定义目录时，在 workbench 后加原 --data-dir。"#;

const WORKBENCH_RECOMMEND_STATUS_HELP: &str = r#"自动推荐当前生命周期状态及下一步
  yss workbench recommend-status --spec-id "<实际 Spec ID>" --json

自动核对登记工程的当前来源、目标和已保存证据，无需交互输入，只读返回推荐。
recommendedStatus、recommendedStage 和 validity 给出建议状态、阶段和证据有效性。
停用、来源不可用、内容无法核实或缺少目标时，建议状态和阶段为 null，
已有交付完成但当前证据需复核时，也保留历史 observedStatus，建议状态和阶段为 null。
recommendedAction 给出下一步。autoConfirm 表示当前主要阶段可用 lifecycle-auto-confirm 写入。
使用自定义目录时，在 workbench 后加原 --data-dir。"#;

const WORKBENCH_LIFECYCLE_HELP: &str = r#"  yss workbench lifecycle --spec "<实际 Spec ID>" --json

result.spec.record 和 result.stages 各项的 record 保存个人标记，各阶段的 taskCounts 为任务计数。
result.progress.stages 展示基于实际证据的有效阶段进度，result.progressHistory 保留目标、报告、更正和决定。
result.spec.content.artifactDigest 为当前产物摘要。已登记目标的采用摘要在 result.progress.goal.specDigest，
目标版本在 result.progress.goalVersion。使用自定义目录时，在 workbench 后加原 --data-dir。"#;

const WORKBENCH_PROGRESS_HELP: &str = r#"先用 yss workbench lifecycle --spec "<实际 Spec ID>" --json 核对当前产物及已登记目标。
用 set-goal 登记目标，再读 lifecycle 取得目标版本后上报实际进展。证据符合条件时自动记录生命周期确认。
需要记录用户明确决定时可使用 confirm。
参与工程或产物变化后使用 reconcile，再读 lifecycle 核对影响。
使用自定义目录时，每条命令都在 workbench 后加原 --data-dir。
写入时用稳定的 --request-id，同一写入重试沿用原 ID 和原输入。"#;

const PROGRESS_SET_GOAL_HELP: &str = r#"先运行 yss workbench lifecycle --spec "<实际 Spec ID>" --json。
使用自定义目录时，每条命令都在 workbench 后加原 --data-dir。
specId 取 result.spec.record.id，specDigest 取 result.spec.content.artifactDigest。
使用 snapshot 时，在 result.specs 中按 record.id 定位同一 Spec，再取该项的 content.artifactDigest。

将以下 JSON 保存为 goal.json，替换占位值并按实际目标填写。
{
  "specId": "<实际 Spec ID>",
  "scope": "<本次已授权范围>",
  "target": {"kind": "local", "label": "<本次本地终点>"},
  "stages": [
    {"stage": "spec", "applicable": true, "reason": "<适用依据>"}
  ],
  "participants": [
    {"projectId": "<实际 Spec 所属工程 ID>", "role": "spec", "label": "<工程名称>"}
  ],
  "acceptanceRefs": ["<原任务或 FR/AC 的实际引用>"],
  "specDigest": "<当前实际 Spec 摘要>"
}

  yss workbench progress set-goal --input goal.json --request-id "<本次写入的稳定请求 ID>" --json

target.kind 可用 local、merged、environment。environment 须填 target.reference 指明实际环境。
stage 可用 research、analysis、spec、prototype、handoff、implementation、verification、delivery。
每阶段只列一次，至少一项 applicable 为 true。applicable 为 false 时填写 reason。
participants 使用已登记的工程 ID，见 snapshot 的 result.projects 各项的 record.id。
role 可用 spec、backend、frontend、integration。spec 角色的 projectId 取 result.spec.record.projectId。
相同写入重试沿用 --request-id 和原 JSON。保存后从 lifecycle 的 result.progress.goalVersion 读取目标版本。"#;

const PROGRESS_REPORT_HELP: &str = r#"先运行 yss workbench lifecycle --spec "<实际 Spec ID>" --json。
使用自定义目录时，每条命令都在 workbench 后加原 --data-dir。
specId 取 result.spec.record.id，goalVersion 取 result.progress.goalVersion，
specDigest 取 result.progress.goal.specDigest。选择已登记目标中的适用阶段。

将以下 JSON 保存为 report.json，替换占位值并按实际进展填写。
{
  "id": "<本次报告的稳定 ID>",
  "specId": "<实际 Spec ID>",
  "goalVersion": "<实际目标版本>",
  "specDigest": "<目标采用的 Spec 摘要>",
  "stage": "spec",
  "role": "spec",
  "progress": "doing",
  "validity": "current",
  "summary": "<已取得的实际进展>"
}

  yss workbench progress report --input report.json --request-id "<本次写入的稳定请求 ID>" --json

stage 可用 research、analysis、spec、prototype、handoff、implementation、verification、delivery。
role 为 spec 时可省略 projectId。backend、frontend、integration 须填目标中与该角色匹配的实际工程 ID。
progress 可用 unmarked、todo、doing、blocked、done、skipped。blocked 须填 blocker，skipped 须填 scopeRefs。
validity 可用 current、unverified、needsSync、needsReview。
在 evidence 中记录实际证据项的 kind、label、reference 及可选的 outcome。
kind 可用 document、artifact、command、test、merge、environment、acceptance、other。
outcome 可用 passed、failed、observed。按需使用以下命令采集检查前的实际输入。

  yss workbench progress observe-inputs --project "<实际工程 ID>" --path "<工程相对路径>" --json

把返回的 result.inputs 和 result.version 分别加入 inputPaths 和 versions 数组，保留原 observedDigest。
相同写入重试沿用报告 id、--request-id 和原 JSON。新报告使用新的 id 和 --request-id。"#;

const PROGRESS_CONFIRM_HELP: &str = r#"需要记录用户明确决定时，先运行 yss workbench lifecycle --spec "<实际 Spec ID>" --json。
也可运行 yss workbench lifecycle-confirm --help，查看直接传入确认字段的用法。
使用自定义目录时，每条命令都在 workbench 后加原 --data-dir。
specId 取 result.spec.record.id，goalVersion 取 result.progress.goalVersion。
scope 与 result.progress.goal.scope 逐字相同，evidenceDigest 取用户本次核对的 result.progress.evidenceDigest。

将以下 JSON 保存为 confirmation.json，按用户已明确表达的实际决定替换占位值。
{
  "specId": "<实际 Spec ID>",
  "goalVersion": "<当前目标版本>",
  "stage": "verification",
  "scope": "<用户核对的当前完整目标范围>",
  "evidenceDigest": "<用户核对的当前证据摘要>",
  "source": "<实际确认人或原始答复出处>",
  "decision": "confirm"
}

  yss workbench progress confirm --input confirmation.json --request-id "<本次写入的稳定请求 ID>" --json

stage 可用 spec（规格）、verification（业务验收）、delivery（最终交付）。
decision 可用 confirm 或 reject。确认覆盖当前目标的完整范围。
证据变化时先重读 lifecycle，按用户对相应范围和证据的实际决定填写。
相同写入重试沿用 --request-id 和原 JSON。保存后可查 lifecycle 的 result.progressHistory.decisions。"#;

const PROGRESS_OBSERVE_INPUTS_HELP: &str = r#"在执行检查前采集该检查采用的实际工程输入
  yss workbench progress observe-inputs --project "<实际工程 ID>" --path "src" --path "config/check.yml" --json

--project 使用已登记工程的 ID，见 snapshot 的 result.projects 各项的 record.id。
替换示例路径，可重复传入工程相对文件或目录。使用自定义目录时，在 workbench 后加原 --data-dir。
把 result.inputs 和 result.version 分别作为条目加入 report 的 inputPaths 和 versions 数组。
保留检查前的 observedDigest。输入发生变化后重新采集并执行检查。"#;

const PROGRESS_RECONCILE_HELP: &str = r#"Spec、参与工程或采用版本变化后，写入一次复核请求。证据符合条件时自动记录生命周期确认。
  yss workbench progress reconcile --spec-id "<实际 Spec ID>" --request-id "<本次复核的稳定请求 ID>" --json
  yss workbench lifecycle --spec "<同一实际 Spec ID>" --json

相同写入重试沿用原 --request-id。使用自定义目录时，两条命令都在 workbench 后加原 --data-dir。
在 lifecycle 的 result.progress 核对有效进度及受影响项，历史依据可查 result.progressHistory。"#;

const REVIEW_MATERIALS_HELP: &str = r#"先运行 yss workbench lifecycle --spec "<实际 Spec ID>" --json。
使用自定义目录时，每条命令都在 workbench 后加原 --data-dir。
specId 取 result.spec.record.id，observedDigest 取 result.spec.content.materialReviewDigest。
摘要缺失时记录材料核实待同步。
材料路径取 result.spec.resources 各项的 record.target 或 result.spec.content.materialChanges 各项的 target。

将以下 JSON 保存为 material-review.json，替换占位值并按实际核实结果填写。
{
  "specId": "<实际 Spec ID>",
  "observedDigest": "<当前材料核实摘要>",
  "reason": "<具体核实依据>",
  "adoptLatest": ["<已关联且当前可读的本地材料路径>"]
}

  yss workbench review-materials --input material-review.json --request-id "<本次写入的稳定请求 ID>" --json

adoptLatest 选择当前可读的本地材料新版。keepConfirmed 用于仍采用原确认版本的共用资料或依赖，
须有原确认且当前材料可核对。每个路径只选一种处理方式。
普通核实省略 bindings，沿用现有绑定。调整时传入完整显式绑定列表，usage 可用 reference、
follow、dependency。dependency 须填 scopeRef，可为文本指定 section 的 start 和 end。
相同写入重试沿用 --request-id 和原 JSON。新版内容确认由用户在工作台完成。"#;

#[derive(Args)]
pub struct ProgressInputArgs {
    #[arg(long, help = "JSON 文件路径，使用 - 从标准输入读取，最大 1 MiB")]
    pub input: String,
    #[arg(long, help = "稳定请求 ID，重试时沿用同一 ID")]
    pub request_id: String,
}

#[derive(Args)]
pub struct LifecycleConfirmArgs {
    #[arg(long, help = "已登记的实际 Spec ID")]
    spec_id: String,
    #[arg(long, help = "用户核对的目标版本，取 progress.goalVersion")]
    goal_version: String,
    #[arg(long, value_enum, help = "用户本次决定对应的阶段")]
    stage: LifecycleConfirmationStage,
    #[arg(long, help = "用户核对的完整范围，须与 progress.goal.scope 逐字相同")]
    scope: String,
    #[arg(long, help = "用户核对的证据摘要，取 progress.evidenceDigest")]
    evidence_digest: String,
    #[arg(long, help = "实际确认人或确定该决定的答复出处")]
    source: String,
    #[arg(long, value_enum, help = "用户已明确给出的确认或退回决定")]
    decision: ConfirmationDecision,
    #[arg(long, help = "稳定请求 ID，重试时沿用同一 ID")]
    request_id: String,
}

#[derive(Clone, clap::ValueEnum)]
enum LifecycleConfirmationStage {
    Spec,
    Verification,
    Delivery,
}

impl LifecycleConfirmArgs {
    fn action(&self) -> (Action, String) {
        let stage = match self.stage {
            LifecycleConfirmationStage::Spec => Stage::Spec,
            LifecycleConfirmationStage::Verification => Stage::Verification,
            LifecycleConfirmationStage::Delivery => Stage::Delivery,
        };
        (
            Action::ConfirmProgress {
                input: ConfirmationInput {
                    spec_id: self.spec_id.clone(),
                    goal_version: self.goal_version.clone(),
                    stage,
                    scope: self.scope.clone(),
                    evidence_digest: self.evidence_digest.clone(),
                    source: self.source.clone(),
                    decision: self.decision,
                },
            },
            self.request_id.clone(),
        )
    }
}

fn progress_input<T: serde::de::DeserializeOwned>(
    input: &str,
    help: &str,
    required_fields: &[&str],
) -> Result<T> {
    const LIMIT: u64 = 1024 * 1024;
    let bytes = if input == "-" {
        let mut bytes = Vec::new();
        std::io::stdin().take(LIMIT + 1).read_to_end(&mut bytes)?;
        bytes
    } else {
        let path = crate::fs::absolute(std::path::Path::new(input))?;
        let parent = path
            .parent()
            .ok_or_else(|| YssError::new(ErrorCode::InvalidInput, "输入路径无效"))?;
        let name = path
            .file_name()
            .and_then(|name| name.to_str())
            .ok_or_else(|| YssError::new(ErrorCode::InvalidInput, "输入文件名无效"))?;
        crate::fs::Root::open(&std::fs::canonicalize(parent)?)?
            .read_checked(&crate::model::RelPath::new(name)?, LIMIT)?
            .bytes
    };
    crate::codec::decode_json(
        &bytes,
        crate::codec::Limits {
            bytes: LIMIT,
            ..crate::codec::Limits::manifest()
        },
    )
    .map_err(|mut error| {
        // Only typed decode errors carry a path once strict JSON validation has passed.
        let Some(path) = error.path.as_deref() else {
            return error;
        };
        if path == "."
            && let Ok(serde_json::Value::Object(object)) = serde_json::from_slice(&bytes)
        {
            let missing: Vec<_> = required_fields
                .iter()
                .copied()
                .filter(|field| !object.contains_key(*field))
                .collect();
            if !missing.is_empty() {
                error.message = format!("缺少必填字段 {}", missing.join("、"));
            }
        }
        error.message.push_str(&format!("。请运行 {help} 查看示例"));
        error
    })
}

fn progress_action(
    command: &WorkbenchProgressCommand,
) -> Result<(crate::workbench::model::Action, String)> {
    use crate::workbench::model::Action;
    Ok(match command {
        WorkbenchProgressCommand::SetGoal(args) => (
            Action::SetLifecycleGoal {
                input: progress_input(
                    &args.input,
                    "yss workbench progress set-goal --help",
                    &["specId", "scope", "target", "specDigest"],
                )?,
            },
            args.request_id.clone(),
        ),
        WorkbenchProgressCommand::Report(args) => (
            Action::ReportProgress {
                input: progress_input(
                    &args.input,
                    "yss workbench progress report --help",
                    &[
                        "id",
                        "specId",
                        "goalVersion",
                        "specDigest",
                        "stage",
                        "role",
                        "progress",
                        "validity",
                        "summary",
                    ],
                )?,
            },
            args.request_id.clone(),
        ),
        WorkbenchProgressCommand::Confirm(args) => (
            Action::ConfirmProgress {
                input: progress_input(
                    &args.input,
                    "yss workbench progress confirm --help",
                    &[
                        "specId",
                        "goalVersion",
                        "stage",
                        "scope",
                        "evidenceDigest",
                        "source",
                        "decision",
                    ],
                )?,
            },
            args.request_id.clone(),
        ),
        WorkbenchProgressCommand::Reconcile {
            spec_id,
            request_id,
        } => (
            Action::ReconcileProgress {
                spec_id: spec_id.clone(),
            },
            request_id.clone(),
        ),
        WorkbenchProgressCommand::ObserveInputs { .. } => unreachable!("observation is read-only"),
    })
}
#[derive(Args)]
pub struct WorkbenchResponseArgs {
    #[arg(long)]
    pub spec_id: String,
    #[arg(long)]
    pub question_id: String,
    #[arg(long, help = "当前问题摘要，见 snapshot 或 request")]
    pub question_digest: String,
    #[arg(long = "kind", value_enum, default_value = "answer")]
    pub response_kind: crate::workbench::model::ResponseKind,
    #[arg(long, default_value = "")]
    pub answer: String,
    #[arg(long, default_value = "")]
    pub reason: String,
    #[arg(long, default_value = "")]
    pub impact: String,
    #[arg(long, default_value = "")]
    pub excluded_scope: String,
    #[arg(long, default_value = "")]
    pub review_at: String,
    #[arg(long, default_value = "")]
    pub review_when: String,
    #[arg(long, default_value_t = 0, allow_hyphen_values = true)]
    pub review_utc_offset_minutes: i16,
}
#[derive(Args)]
pub struct WorkbenchQueryArgs {
    #[arg(long, value_enum, default_value = "personal", help = "Spec 进度口径")]
    pub progress_basis: crate::workbench::progress::ProgressBasis,
    #[arg(
        long,
        value_parser = ["awaitingConfirmation", "unverified", "needsSync", "needsReview", "conflict"],
        help = "有效进度关注项，可重复指定"
    )]
    pub attention: Vec<String>,
    #[arg(long, value_enum, default_value = "spec", help = "查询 Spec 或任务")]
    pub entity: crate::workbench::model::WorkEntity,
    #[arg(long, default_value = "", help = "按标题、主文件路径等字段搜索关键词")]
    pub q: String,
    #[arg(long = "project", help = "按已登记工程 ID 筛选，可重复指定")]
    pub project_ids: Vec<String>,
    #[arg(long = "spec", help = "按已登记 Spec ID 筛选，可重复指定")]
    pub spec_ids: Vec<String>,
    #[arg(long = "stage", value_enum)]
    pub stages: Vec<crate::workbench::model::Stage>,
    #[arg(long = "status", value_enum)]
    pub statuses: Vec<crate::workbench::model::Status>,
    #[arg(long = "content-status", value_enum)]
    pub content_statuses: Vec<crate::workbench::model::ContentStatus>,
    #[arg(long="reminder",value_parser=["decision","research","overdue","writeback","ready"])]
    pub reminder_kinds: Vec<String>,
    #[arg(long)]
    pub needs_reconfirmation: bool,
    #[arg(long, value_enum, default_value = "active", help = "按启用状态筛选")]
    pub activity: crate::workbench::model::ActivityScope,
    #[arg(long, help = "包含已完成及已取消项")]
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
    #[arg(long, help = "目标项目的根目录")]
    pub root: PathBuf,
    #[arg(
        long,
        value_enum,
        help = "项目类型：spec 业务规格、产品设计与产品 Home 工程会话入口；backend 后端研发；frontend 前端研发"
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
    #[arg(long, default_value = ".", help = "本次操作的根目录")]
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
        about = "只读列出当前 Profile 可用的技能及其项目安装状态",
        after_help = "示例\n  yss skills list --root ./my-project --json\n\n--root 指向已接入 YSS 的项目根目录，省略时使用当前目录。\nresult.available 列出可用技能，result.installed 按项目安装收据和 .agents/skills 下的实际文件列出状态。\n宿主还可通过个人目录或链接发现技能。要核实宿主能否调用，请检查该宿主启用的入口及其来源。"
    )]
    List(RootArgs),
    #[command(
        about = "补装指定技能及其依赖资源",
        after_help = "示例\n  yss skills list --root ./my-project --json\n  yss skills ensure setup-yss-harness --root ./my-project --dry-run\n\n显式指定 --root 为已接入 YSS 的项目根目录，省略时使用当前目录。\n从同一目录的 skills list 结果选择一个或多个可用技能，去掉 --dry-run 补装。"
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
        after_help = "示例：\n  yss scaffold frontend --project-name my-frontend --output-dir . --dry-run\n\n输出父目录必须已存在，目标工程必须不存在。去掉 --dry-run 生成源码；需要技能时使用本次已核验的 CLI 执行 nextSteps 中的 attach skills，运行按任务另行安装依赖。完整工程接入由 Agent 按 setup-yss-harness 的 references/project-onboarding.md 检查 Git 边界，复用或按授权创建本地仓，核实远程、Issue tracker 和按需 Home；scaffold 不执行这些配置。"
    )]
    Frontend(FrontendScaffoldArgs),
    #[command(about = "查看可选平台、版本与配方")]
    Catalog,
    #[command(
        about = "生成 DDD 分层 Java 空工程",
        after_help = "默认配方（技术参数按 catalog 条件补全）\n  yss scaffold ddd --project-name sample-app --base-package com.example.sample --output-dir . --group-id com.example --project-version 1.0.0-SNAPSHOT --dry-run\n\n选择 3.2\n  yss scaffold ddd --project-name sample-app --base-package com.example.sample --output-dir . --group-id com.example --project-version 1.0.0-SNAPSHOT --yss-components-version 3.2.0-SNAPSHOT --dry-run\n\n完整自定义组合\n  yss scaffold ddd --project-name sample-app --base-package com.example.sample --output-dir . --group-id com.example --project-version 1.0.0-SNAPSHOT --platform-profile spring-boot-3.5-jdk17 --spring-boot-version 3.5.16 --java-version 17 --parent-group-id com.example --parent-artifact-id custom-parent --parent-version 1.0.0 --yss-components-version 3.2.0-SNAPSHOT --dry-run\n\n显式值优先，仅补全唯一组合，有歧义时按提示补充参数。先运行 yss scaffold catalog 查看默认配方与版本。输出目录下不能已有同名工程。需要技能时使用本次已核验的 CLI 执行 nextSteps 中的 attach --profile backend --installation skills。完整工程接入由 Agent 按 setup-yss-harness 的 references/project-onboarding.md 检查 Git 边界，复用或按授权创建本地仓，核实远程、Issue tracker 和按需 Home；scaffold 不执行这些配置。"
    )]
    Ddd(ScaffoldArgs),
    #[command(
        about = "生成 MVC 分层 Java 空工程",
        after_help = "默认配方（技术参数按 catalog 条件补全）\n  yss scaffold mvc --project-name sample-app --base-package com.example.sample --output-dir . --group-id com.example --project-version 1.0.0-SNAPSHOT --dry-run\n\n选择 3.2\n  yss scaffold mvc --project-name sample-app --base-package com.example.sample --output-dir . --group-id com.example --project-version 1.0.0-SNAPSHOT --yss-components-version 3.2.0-SNAPSHOT --dry-run\n\n完整自定义组合\n  yss scaffold mvc --project-name sample-app --base-package com.example.sample --output-dir . --group-id com.example --project-version 1.0.0-SNAPSHOT --platform-profile spring-boot-3.5-jdk17 --spring-boot-version 3.5.16 --java-version 17 --parent-group-id com.example --parent-artifact-id custom-parent --parent-version 1.0.0 --yss-components-version 3.2.0-SNAPSHOT --dry-run\n\n显式值优先，仅补全唯一组合，有歧义时按提示补充参数。先运行 yss scaffold catalog 查看默认配方与版本。输出目录下不能已有同名工程。需要技能时使用本次已核验的 CLI 执行 nextSteps 中的 attach --profile backend --installation skills。完整工程接入由 Agent 按 setup-yss-harness 的 references/project-onboarding.md 检查 Git 边界，复用或按授权创建本地仓，核实远程、Issue tracker 和按需 Home；scaffold 不执行这些配置。"
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
    pub platform_profile: Option<String>,
    #[arg(long, help = "与平台匹配的精确 Spring Boot 版本")]
    pub spring_boot_version: Option<String>,
    #[arg(long, help = "与平台匹配的 Java 主版本，例如 17")]
    pub java_version: Option<u32>,
    #[arg(long, help = "生成工程的 Maven groupId")]
    pub group_id: String,
    #[arg(long, help = "生成工程的 Maven 版本")]
    pub project_version: String,
    #[arg(long, help = "父 POM 的 Maven groupId")]
    pub parent_group_id: Option<String>,
    #[arg(long, help = "父 POM 的 Maven artifactId")]
    pub parent_artifact_id: Option<String>,
    #[arg(long, help = "父 POM 的精确版本")]
    pub parent_version: Option<String>,
    #[arg(long, help = "YSS 组件 BOM 的精确版本")]
    pub yss_components_version: Option<String>,
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
    WorkbenchMutation(crate::workbench::model::MutationResult),
    WorkbenchRequest(crate::workbench::model::AgentRequest),
    WorkbenchQuery(crate::workbench::model::QueryResult),
    WorkbenchStatus(serde_json::Value),
    WorkbenchLifecycle(Box<crate::workbench::model::LifecycleView>),
    WorkbenchInputs(crate::workbench::progress::InputObservation),
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
            match &args.command {
                Some(WorkbenchCommand::Progress(WorkbenchProgressCommand::ObserveInputs {
                    project,
                    paths,
                })) => {
                    return Ok(Report::WorkbenchInputs(
                        crate::workbench::progress::observe_inputs(&path, project, paths, cancel)?,
                    ));
                }
                Some(WorkbenchCommand::Progress(_))
                | Some(WorkbenchCommand::ReviewMaterials(_))
                | Some(WorkbenchCommand::LifecycleAutoConfirm { .. })
                | Some(WorkbenchCommand::LifecycleConfirm(_)) => {
                    let (action, request_id) = match args.command.as_ref().expect("write command") {
                        WorkbenchCommand::Progress(command) => progress_action(command)?,
                        WorkbenchCommand::LifecycleConfirm(input) => input.action(),
                        WorkbenchCommand::LifecycleAutoConfirm {
                            spec_id,
                            request_id,
                        } => (
                            Action::ReconcileProgress {
                                spec_id: spec_id.clone(),
                            },
                            request_id.clone(),
                        ),
                        WorkbenchCommand::ReviewMaterials(input) => (
                            crate::workbench::model::Action::ReviewMaterials {
                                input: progress_input(
                                    &input.input,
                                    "yss workbench review-materials --help",
                                    &["specId", "observedDigest", "reason"],
                                )?,
                            },
                            input.request_id.clone(),
                        ),
                        _ => unreachable!("write command was matched"),
                    };
                    if let Some(result) = crate::workbench::session::mutate_with_request_if_running(
                        &path,
                        action.clone(),
                        &request_id,
                    )? {
                        return Ok(Report::WorkbenchMutation(result));
                    }
                    let mut store = crate::workbench::store::Store::open(&path)?;
                    let expected_revision = store.state().revision;
                    let result = crate::workbench::service::mutate(
                        &mut store,
                        crate::workbench::model::MutationRequest {
                            expected_revision,
                            expected_target: None,
                            request_id: Some(request_id),
                            action,
                        },
                    )?;
                    return Ok(Report::WorkbenchMutation(result));
                }
                Some(WorkbenchCommand::Register { root, source }) => {
                    let root = crate::fs::absolute(root)?;
                    let root = root.to_str().ok_or_else(|| {
                        YssError::new(ErrorCode::InvalidInput, "项目路径不是 UTF-8")
                    })?;
                    if let Some(result) =
                        crate::workbench::session::register_if_running(&path, root, source)?
                    {
                        return Ok(Report::WorkbenchMutation(result));
                    }
                    let mut store = crate::workbench::store::Store::open(&path)?;
                    let revision = store.state().revision;
                    return Ok(Report::WorkbenchMutation(
                        crate::workbench::registration::register(
                            &mut store, root, source, revision,
                        )?,
                    ));
                }
                Some(WorkbenchCommand::Request { spec_id }) => {
                    return Ok(Report::WorkbenchRequest(
                        crate::workbench::service::read_agent_request(&path, spec_id, cancel)?,
                    ));
                }
                Some(WorkbenchCommand::SpecStatus { spec_id }) => {
                    return Ok(Report::WorkbenchStatus(crate::workbench::status::read(
                        &path,
                        spec_id,
                        ReportKind::Spec,
                        cancel,
                    )?));
                }
                Some(WorkbenchCommand::LifecycleStatus { spec_id }) => {
                    return Ok(Report::WorkbenchStatus(crate::workbench::status::read(
                        &path,
                        spec_id,
                        ReportKind::Lifecycle,
                        cancel,
                    )?));
                }
                Some(WorkbenchCommand::RecommendStatus { spec_id }) => {
                    return Ok(Report::WorkbenchStatus(crate::workbench::status::read(
                        &path,
                        spec_id,
                        ReportKind::Recommendation,
                        cancel,
                    )?));
                }
                Some(WorkbenchCommand::Respond(r)) => {
                    let action = crate::workbench::model::Action::RespondQuestion {
                        spec_id: r.spec_id.clone(),
                        question_id: r.question_id.clone(),
                        question_digest: r.question_digest.clone(),
                        response_kind: r.response_kind,
                        answer: r.answer.clone(),
                        reason: r.reason.clone(),
                        impact: r.impact.clone(),
                        excluded_scope: r.excluded_scope.clone(),
                        review_at: r.review_at.clone(),
                        review_when: r.review_when.clone(),
                        review_utc_offset_minutes: r.review_utc_offset_minutes,
                    };
                    if let Some(result) =
                        crate::workbench::session::mutate_if_running(&path, action.clone())?
                    {
                        return Ok(Report::WorkbenchMutation(result));
                    }
                    let mut store = crate::workbench::store::Store::open(&path)?;
                    let expected_revision = store.state().revision;
                    return Ok(Report::WorkbenchMutation(
                        crate::workbench::service::mutate(
                            &mut store,
                            crate::workbench::model::MutationRequest {
                                expected_revision,
                                expected_target: None,
                                request_id: None,
                                action,
                            },
                        )?,
                    ));
                }
                _ => {}
            }
            let snapshot = crate::workbench::service::read_snapshot(&path, cancel)?;
            match args.command {
                Some(WorkbenchCommand::Query(q)) => {
                    Ok(Report::WorkbenchQuery(crate::workbench::query::query(
                        &snapshot,
                        &crate::workbench::model::WorkQuery {
                            progress_basis: q.progress_basis,
                            attention: q.attention,
                            content_statuses: q.content_statuses,
                            reminder_kinds: q.reminder_kinds,
                            needs_reconfirmation: q.needs_reconfirmation,
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
                Some(WorkbenchCommand::Lifecycle { spec }) => {
                    Ok(Report::WorkbenchLifecycle(Box::new(
                        crate::workbench::lifecycle::build_lifecycle(&snapshot, &spec)?,
                    )))
                }
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
    use crate::scaffold::{ScaffoldInputOptions, ScaffoldRequest};
    let bundle = crate::bundle::load(bundle)?;
    let request = ScaffoldRequest {
        architecture,
        output_dir: args.output_dir,
        dry_run: args.dry_run,
        input: crate::scaffold::resolve_inputs(
            &bundle,
            &ScaffoldInputOptions {
                project_name: args.project_name,
                base_package: args.base_package,
                platform_profile: args.platform_profile,
                spring_boot_version: args.spring_boot_version,
                java_version: args.java_version,
                group_id: args.group_id,
                project_version: args.project_version,
                parent_group_id: args.parent_group_id,
                parent_artifact_id: args.parent_artifact_id,
                parent_version: args.parent_version,
                yss_components_version: args.yss_components_version,
            },
        )?,
    };
    Ok(Report::Scaffold(crate::scaffold::generate(
        &bundle, &request, cancel,
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

fn context_strings(error: &clap::Error, kind: ContextKind) -> &[String] {
    match error.get(kind) {
        Some(ContextValue::String(value)) => std::slice::from_ref(value),
        Some(ContextValue::Strings(values)) => values,
        _ => &[],
    }
}

fn public_arg<'a>(command: &'a clap::Command, label: &str) -> Option<&'a clap::Arg> {
    let long = label.strip_prefix("--");
    command.get_arguments().find(|arg| {
        !arg.is_hide_set()
            && (arg.to_string() == label || long.is_some_and(|name| arg.get_long() == Some(name)))
    })
}

fn arg_label(arg: &clap::Arg) -> String {
    if let Some(long) = arg.get_long() {
        format!("--{long}")
    } else if let Some(short) = arg.get_short() {
        format!("-{short}")
    } else {
        arg.to_string()
    }
}

fn argument_error(root: &clap::Command, error: &clap::Error) -> YssError {
    let mut command = root;
    let mut command_path = root.get_name().to_owned();
    // Clap sets the binary name of each subcommand it actually enters during parsing.
    while let Some(child) = command
        .get_subcommands()
        .find(|sc| sc.get_bin_name().is_some())
    {
        command = child;
        command_path.push(' ');
        command_path.push_str(command.get_name());
    }
    // Built-in help visits targets in a clone, so validate the usage prefix against the command tree.
    if error.kind() == ErrorKind::InvalidSubcommand
        && let Some(ContextValue::StyledStr(usage)) = error.get(ContextKind::Usage)
    {
        let usage = usage.to_string();
        let mut tokens = usage.split_whitespace();
        if tokens.next() == Some("Usage:")
            && command_path
                .split_whitespace()
                .all(|name| tokens.next() == Some(name))
        {
            for token in tokens {
                let Some(child) = command.get_subcommands().find(|child| {
                    child.get_name() == token && token != "help" && !child.is_hide_set()
                }) else {
                    break;
                };
                command = child;
                command_path.push(' ');
                command_path.push_str(command.get_name());
            }
        }
    }

    let operands = |kind| {
        context_strings(error, kind)
            .iter()
            .filter_map(|label| public_arg(command, label))
            .map(arg_label)
            .collect::<Vec<_>>()
            .join("、")
    };
    let args = operands(ContextKind::InvalidArg);
    let subject = if args.is_empty() {
        "参数".to_owned()
    } else {
        format!("参数 {args}")
    };
    let mut message = match error.kind() {
        ErrorKind::MissingRequiredArgument if !args.is_empty() => format!("缺少必填参数 {args}"),
        ErrorKind::MissingRequiredArgument => "缺少必填参数".into(),
        ErrorKind::MissingSubcommand => "缺少子命令".into(),
        ErrorKind::ArgumentConflict => {
            let prior = operands(ContextKind::PriorArg);
            if !args.is_empty() && args == prior {
                format!("{subject} 重复出现")
            } else if !args.is_empty() && !prior.is_empty() {
                format!("{subject} 与 {prior} 不能同时使用")
            } else {
                "参数不能同时使用".into()
            }
        }
        ErrorKind::InvalidValue | ErrorKind::ValueValidation => {
            let missing = error.kind() == ErrorKind::InvalidValue
                && context_strings(error, ContextKind::InvalidValue)
                    .first()
                    .is_some_and(String::is_empty);
            let mut message = if missing {
                format!("{subject} 缺少值")
            } else {
                format!("{subject} 的值无效")
            };
            if let Some(arg) = context_strings(error, ContextKind::InvalidArg)
                .first()
                .and_then(|label| public_arg(command, label))
            {
                let choices = arg.get_possible_values();
                let choices: Vec<_> = choices
                    .iter()
                    .filter(|choice| !choice.is_hide_set())
                    .map(|choice| choice.get_name())
                    .collect();
                if !choices.is_empty() {
                    message.push_str(&format!("，可选值为 {}", choices.join("、")));
                }
            }
            message
        }
        ErrorKind::NoEquals => format!("{subject} 的值须用等号连接"),
        ErrorKind::TooFewValues => format!("{subject} 缺少值"),
        ErrorKind::TooManyValues | ErrorKind::WrongNumberOfValues => {
            format!("{subject} 的值数量不正确")
        }
        ErrorKind::InvalidSubcommand => {
            let suggestions: Vec<_> = context_strings(error, ContextKind::SuggestedSubcommand)
                .iter()
                .filter_map(|name| command.find_subcommand(name))
                .filter(|child| !child.is_hide_set())
                .map(clap::Command::get_name)
                .collect();
            if suggestions.is_empty() {
                "无法识别子命令".into()
            } else {
                format!("无法识别子命令，可用子命令为 {}", suggestions.join("、"))
            }
        }
        ErrorKind::UnknownArgument => {
            let suggestions = operands(ContextKind::SuggestedArg);
            if suggestions.is_empty() {
                "无法识别参数".into()
            } else {
                format!("无法识别参数，可用参数为 {suggestions}")
            }
        }
        ErrorKind::InvalidUtf8 => "参数包含无效的 UTF-8 文本".into(),
        _ => "参数不正确".into(),
    };
    let help_command = format!("{command_path} --help");
    message.push_str(&format!("。请运行 {help_command} 查看用法"));
    YssError::new(ErrorCode::Argument, message).context("helpCommand", help_command)
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
    let mut parser = Cli::command();
    let parsed = parser
        .try_get_matches_from_mut(std::iter::once(OsString::from("yss")).chain(args))
        .and_then(|mut matches| Cli::from_arg_matches_mut(&mut matches));
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
                                    bundle_bytes,
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
                    help: parser.render_long_help().to_string(),
                }),
            ),
        },
        Err(e) if e.kind() == ErrorKind::DisplayHelp => (
            "help",
            Ok(Report::Help {
                help: e.to_string(),
            }),
        ),
        Err(error) => ("arguments", Err(argument_error(&parser, &error))),
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
