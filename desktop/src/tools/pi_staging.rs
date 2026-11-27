use semver::Version;
use std::{path::Path, process::Command, time::Duration};
use yss::{
    cancel::Cancellation,
    error::YssError,
    fs::Root,
    model::RelPath,
    workbench::{
        agent_protocol::probe_managed_pi_update,
        managed_pi::{self, NodeRecord},
        process::capture_command,
    },
};

pub(in crate::tools) struct PiApplyError {
    pub(in crate::tools) uncertain: bool,
    pub(in crate::tools) message: String,
}

impl PiApplyError {
    pub(super) fn publishing(
        error: YssError,
        not_published: &'static str,
        uncertain: &'static str,
    ) -> Self {
        if error
            .context
            .get("renameApplied")
            .is_some_and(|applied| applied == "true")
        {
            Self {
                uncertain: true,
                message: uncertain.into(),
            }
        } else {
            not_published.into()
        }
    }
}

impl From<&str> for PiApplyError {
    fn from(message: &str) -> Self {
        message.to_owned().into()
    }
}

impl From<String> for PiApplyError {
    fn from(message: String) -> Self {
        Self {
            uncertain: false,
            message,
        }
    }
}

pub(super) struct PackageStage<'a> {
    pub root: &'a Root,
    pub node: &'a Path,
    pub npm: &'a Path,
    pub installed_node: &'a Path,
    pub version: &'a Version,
    pub update: bool,
}

impl PackageStage<'_> {
    pub(super) fn install(&self, command: impl Fn(&Path, &Path) -> Command) -> Result<(), String> {
        let stage = &self.root.path;
        for name in ["npm-user.conf", "npm-global.conf"] {
            self.root
                .create_private_file(&RelPath::new(name).map_err(|_| "暂存配置路径无效")?, b"")
                .map_err(|_| "无法创建隔离 npm 配置")?;
        }
        let mut npm = command(self.node, stage);
        npm.arg(self.npm)
            .args([
                "install",
                "--ignore-scripts",
                "--no-audit",
                "--no-fund",
                "--package-lock=false",
                "--registry=https://registry.npmjs.org/",
                "--maxsockets=6",
                "--fetch-timeout=60000",
            ])
            .arg("--prefix")
            .arg(stage)
            .arg("--cache")
            .arg(stage.join(".npm-cache"))
            .arg("--userconfig")
            .arg(stage.join("npm-user.conf"))
            .arg("--globalconfig")
            .arg(stage.join("npm-global.conf"));
        if self.update {
            npm.args(["--engine-strict", "--loglevel=error"]);
            for name in [
                "HTTPS_PROXY",
                "https_proxy",
                "HTTP_PROXY",
                "http_proxy",
                "NO_PROXY",
                "no_proxy",
            ] {
                if let Some(value) = std::env::var_os(name) {
                    npm.env(name, value);
                }
            }
        }
        npm.arg(format!("{}@{}", managed_pi::PACKAGE, self.version));
        let cancel = Cancellation::new();
        let (status, _, stderr) = capture_command(npm, &cancel, Duration::from_secs(900))
            .map_err(|_| "隔离 npm 操作无法启动、超时或输出过多；请检查网络后重试")?;
        if !status.success() {
            if self.update && String::from_utf8_lossy(&stderr).contains("EBADENGINE") {
                return Err("当前 Node 与 Pi 新版本或其依赖不兼容；请准备兼容 Node 后重试".into());
            }
            return Err(format!(
                "Pi 软件包下载未完成（状态 {status}）；请检查网络并重试"
            ));
        }
        let entry =
            managed_pi::verify_package(self.root, self.version).map_err(|error| error.message)?;
        let mut version = command(self.node, stage);
        version.arg(&entry).arg("--version");
        let (status, stdout, _) = capture_command(version, &cancel, Duration::from_secs(15))
            .map_err(|_| "无法验证 Pi 入口")?;
        let output = std::str::from_utf8(&stdout)
            .ok()
            .and_then(|text| Version::parse(text.trim().trim_start_matches('v')).ok());
        if !status.success() || output.as_ref() != Some(self.version) {
            return Err("Pi 入口版本校验失败".into());
        }
        if self.update {
            let mut rpc = command(self.node, stage);
            rpc.arg(&entry);
            probe_managed_pi_update(rpc, &cancel).map_err(|error| {
                format!("Pi 新版本的本机 RPC 兼容性自检未通过：{}", error.message)
            })?;
        }
        let bytes = serde_json::to_vec(&NodeRecord {
            node_path: self.installed_node.into(),
        })
        .map_err(|_| "无法记录 Node 路径")?;
        self.root
            .create_private_file(
                &RelPath::new("node-path.json").map_err(|_| "Node 记录路径无效")?,
                &bytes,
            )
            .map_err(|_| "无法安全记录 Node 路径")?;
        Ok(())
    }
}
