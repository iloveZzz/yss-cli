use crate::model::{InstallationKind, Profile};
use std::path::Path;

#[derive(Debug, serde::Serialize)]
#[serde(rename_all = "camelCase")]
pub struct NextStep {
    pub id: &'static str,
    pub message: &'static str,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub command: Option<CommandHint>,
}

#[derive(Debug, serde::Serialize)]
pub struct CommandHint {
    pub program: &'static str,
    pub args: Vec<String>,
}

pub(crate) fn project_next_steps(
    profile: Profile,
    installation: InstallationKind,
) -> Vec<NextStep> {
    let (id, message) = match (profile, installation) {
        (Profile::Spec, InstallationKind::Project) => (
            "configure-home",
            "若本仓承担 Home 职责，由 Agent 复用已有仓库索引，按已核实的工程身份补齐项目维护的 Home 地图及入口引用；Home 不替代各工程的 Git、远程与 tracker 配置。",
        ),
        (Profile::Spec, InstallationKind::Skills) => (
            "review-repository-role",
            "按当前仓库职责使用 Spec 技能；仅在明确将本仓定位为 Home 时配置仓库关系。",
        ),
        _ => (
            "review-upstream",
            "若本工程有上游 Home 或规格，复用已有入口记录其位置及本仓技能入口；没有上游时无需补造关系。Git 接入与技能共享另见 setup-yss-harness 的 references/project-portability.md。",
        ),
    };
    let mut steps = project_onboarding_steps();
    steps.push(NextStep {
        id,
        message,
        command: None,
    });
    steps
}

fn project_onboarding_steps() -> Vec<NextStep> {
    vec![
        NextStep {
            id: "complete-project-onboarding",
            message: "若本次要求完整工程初始化或接入，由 Agent 按 setup-yss-harness 的 references/project-onboarding.md 检查真实 Git 根及 worktree/submodule；已知远程先核实是否已有内容，再复用适用本地仓，仅独立新工程按授权创建本地仓，并核实或配置已确认的 fetch/push remote。仅安装或补装 CLI/skills 不自动执行这些接入动作，文件安装成功不等于 Git、远程或 tracker 已就绪。",
            command: None,
        },
        NextStep {
            id: "configure-issue-tracker",
            message: "完整工程接入还需核实目标工程 .template-spec/agents/issue-tracker.md：保留已有有效 tracker；尚无已确认 tracker 时优先使用用户明确选择，没有明确选择且已核实唯一 GitHub/GitLab 代码托管项目的新工程默认采用该项目 Issues，并明确报告实例与项目。目标不明时说明缺口，不猜测或回退到其他平台；按需补齐 Home 关系。",
            command: None,
        },
    ]
}

pub(crate) fn scaffold_next_steps(profile: Profile, root: &Path) -> Vec<NextStep> {
    let (name, message) = match profile {
        Profile::Backend => (
            "backend",
            "源码已生成；如需项目技能，使用本次已核验的 CLI 接入 Backend skills，再按 setup-yss-harness 的工程接入参考继续已授权工作。",
        ),
        Profile::Frontend => (
            "frontend",
            "源码已生成；如需项目技能，使用本次已核验的 CLI 接入 Frontend skills，再按 setup-yss-harness 的工程接入参考继续已授权工作。",
        ),
        Profile::Spec => return Vec::new(),
    };
    let mut steps = vec![NextStep {
        id: "attach-skills",
        message,
        command: root.to_str().map(|path| CommandHint {
            program: "yss",
            args: [
                "attach",
                "--root",
                path,
                "--profile",
                name,
                "--installation",
                "skills",
            ]
            .into_iter()
            .map(str::to_owned)
            .collect(),
        }),
    }];
    steps.extend(project_onboarding_steps());
    steps
}
