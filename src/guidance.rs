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
            "方法与技能已安装；由 Agent 检查已有仓库索引、核实工程位置与 Git 身份，按需补齐项目维护的 Home 地图及入口引用。",
        ),
        (Profile::Spec, InstallationKind::Skills) => (
            "review-repository-role",
            "按当前仓库职责使用 Spec 技能；仅在明确将本仓定位为 Home 时配置仓库关系。",
        ),
        _ => (
            "review-upstream",
            "若本工程有上游 Home 或规格，复用已有入口记录其位置及本仓技能入口；Git 接入与技能共享按 setup-yss-harness 的 project-portability 参考处理，没有上游时无需补造关系。",
        ),
    };
    vec![NextStep {
        id,
        message,
        command: None,
    }]
}

pub(crate) fn scaffold_next_steps(profile: Profile, root: &Path) -> Vec<NextStep> {
    let (name, message) = match profile {
        Profile::Backend => (
            "backend",
            "如需项目技能，使用本次已核验的 CLI 接入 Backend skills；随后按实际 Git 边界和已知上游关系完成接入。",
        ),
        Profile::Frontend => (
            "frontend",
            "如需项目技能，使用本次已核验的 CLI 接入 Frontend skills；随后按实际 Git 边界和已知上游关系完成接入。",
        ),
        Profile::Spec => return Vec::new(),
    };
    vec![NextStep {
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
    }]
}
