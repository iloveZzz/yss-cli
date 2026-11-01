mod context;
mod ddd;
mod mvc;
use crate::{
    bundle::Bundle,
    cancel::Cancellation,
    codec,
    error::{ErrorCode, Result, YssError},
    fs::{self, FileSnapshot, Root},
    model::{FileDescriptor, Profile, RelPath, RenderedTree, Sha256, SourceProvenance},
};
pub use context::{Coordinates, GenerationContext};
use serde::{Deserialize, Serialize};
use std::{
    collections::{BTreeMap, BTreeSet},
    path::PathBuf,
};
const ASSETS: &str = ".agents/skills/yss-ddd-scaffold-generator/assets";

#[derive(Debug, Clone, Copy, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Architecture {
    Ddd,
    Mvc,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct ScaffoldInputs {
    pub project_name: String,
    pub base_package: String,
    pub platform_profile: String,
    pub spring_boot_version: String,
    pub java_version: u32,
    pub group_id: String,
    pub project_version: String,
    pub parent_group_id: String,
    pub parent_artifact_id: String,
    pub parent_version: String,
    pub yss_components_version: String,
}
pub struct ScaffoldRequest {
    pub architecture: Architecture,
    pub input: ScaffoldInputs,
    pub output_dir: PathBuf,
    pub dry_run: bool,
}
#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ScaffoldReport {
    pub root: String,
    pub status: &'static str,
    pub dry_run: bool,
    pub files: usize,
    pub modules: Vec<String>,
    pub source: SourceProvenance,
}
#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct CatalogReport {
    pub source: SourceProvenance,
    pub platforms: Vec<PlatformChoice>,
    pub required_flags: Vec<&'static str>,
}
#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct PlatformChoice {
    pub id: String,
    pub spring_boot_version: String,
    pub java_version: u32,
    pub recipes: Vec<RecipeChoice>,
}
#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct RecipeChoice {
    pub id: String,
    pub parent: Coordinates,
    pub bom: Coordinates,
    pub evidence_status: String,
    pub blockers: Vec<String>,
}
#[derive(Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct Receipt {
    format: String,
    format_version: u32,
    architecture: Architecture,
    input: ScaffoldInputs,
    context: GenerationContext,
    source: SourceProvenance,
    bundle_digest: Sha256,
    platform: context::Platform,
    modules: Vec<String>,
    files: BTreeMap<RelPath, FileDescriptor>,
    directories: BTreeSet<RelPath>,
}
pub fn catalog(bundle: &Bundle) -> Result<CatalogReport> {
    let input = context::catalog(bundle)?;
    let platforms = input
        .profiles
        .into_iter()
        .map(|profile| {
            let recipes = input
                .compatibility
                .iter()
                .filter(|r| {
                    r.profile_id == profile.id
                        && r.spring_boot_version == profile.spring_boot_version
                })
                .map(|r| RecipeChoice {
                    id: r.id.clone(),
                    parent: r.parent.clone(),
                    bom: r.bom.clone(),
                    evidence_status: r.status.clone(),
                    blockers: r.blockers.clone(),
                })
                .collect();
            PlatformChoice {
                id: profile.id,
                spring_boot_version: profile.spring_boot_version,
                java_version: profile.java_version,
                recipes,
            }
        })
        .collect();
    Ok(CatalogReport {
        source: bundle.manifest.source.clone(),
        platforms,
        required_flags: vec![
            "project-name",
            "base-package",
            "output-dir",
            "platform-profile",
            "spring-boot-version",
            "java-version",
            "group-id",
            "project-version",
            "parent-group-id",
            "parent-artifact-id",
            "parent-version",
            "yss-components-version",
        ],
    })
}
fn modules(architecture: Architecture) -> Vec<String> {
    match architecture {
        Architecture::Ddd => vec![
            "domain",
            "application",
            "infrastructure",
            "adapter",
            "bootstrap",
        ],
        Architecture::Mvc => vec!["server", "service", "repository"],
    }
    .into_iter()
    .map(str::to_owned)
    .collect()
}
pub fn render(
    bundle: &Bundle,
    request: &ScaffoldRequest,
    context: &GenerationContext,
) -> Result<RenderedTree> {
    context.validate()?;
    let platform = context::validate(bundle, &request.input)?;
    let vars = context::variables(&request.input, &platform, context);
    let mut tree = RenderedTree::default();
    match request.architecture {
        Architecture::Ddd => ddd::render(bundle, &request.input, &vars, &mut tree)?,
        Architecture::Mvc => mvc::render(bundle, &request.input, &platform, &vars, &mut tree)?,
    }
    copy_wrapper(bundle, &mut tree)?;
    if !tree.files.contains_key(&RelPath::new("README.md")?) {
        put(
            &mut tree,
            "README.md",
            format!(
                "# {}\n\nSpring Boot {} / Java {} 的业务空骨架。生成已完成；构建尚未验证。\n\n```sh\n./mvnw -DskipTests package\n```\n",
                request.input.project_name, platform.spring_boot_version, platform.java_version
            ),
        )?;
    }
    for (path, file) in &tree.files {
        if let Ok(text) = std::str::from_utf8(&file.bytes) {
            if text.contains("{{") || text.contains("__YSS_") {
                return Err(
                    YssError::new(ErrorCode::InvalidInput, "生成文件包含未替换占位符").at(path),
                );
            }
            for line in text.lines() {
                let trimmed = line.trim();
                if let Some(value) = trimmed.strip_prefix("password:") {
                    let value = value.trim();
                    if !matches!(value, "" | "\"\"" | "''") && !value.starts_with("${") {
                        return Err(
                            YssError::new(ErrorCode::InvalidInput, "模板包含固定凭据").at(path)
                        );
                    }
                }
                if trimmed.contains("<password>")
                    && !trimmed.contains("${env.")
                    && !trimmed.contains("<password></password>")
                {
                    return Err(
                        YssError::new(ErrorCode::InvalidInput, "模板包含固定 Maven 凭据").at(path),
                    );
                }
            }
        }
    }
    for path in tree.files.keys().chain(tree.directories.clone().iter()) {
        for parent in path.parents() {
            tree.directories.insert(RelPath::new(parent)?);
        }
    }
    let files = tree
        .files
        .iter()
        .map(|(path, file)| {
            (
                path.clone(),
                FileDescriptor::new(&file.bytes, fs::observable_mode(file.mode)),
            )
        })
        .collect();
    let receipt = Receipt {
        format: "yss.scaffold".into(),
        format_version: 1,
        architecture: request.architecture,
        input: request.input.clone(),
        context: context.clone(),
        source: bundle.manifest.source.clone(),
        bundle_digest: bundle.digest.clone(),
        platform,
        modules: modules(request.architecture),
        files,
        directories: tree.directories.clone(),
    };
    tree.insert(
        RelPath::new(".yss/scaffold.json")?,
        codec::encode_json(&receipt)?,
        0o644,
    )?;
    fs::validate_tree(&tree)?;
    Ok(tree)
}
pub fn generate(
    bundle: &Bundle,
    request: &ScaffoldRequest,
    cancel: &Cancellation,
) -> Result<ScaffoldReport> {
    cancel.check()?;
    let tree = render(bundle, request, &GenerationContext::capture()?)?;
    let output = fs::absolute(&request.output_dir)?;
    let target = output.join(&request.input.project_name);
    if output.exists() {
        let root = Root::open(&output)?;
        if root.inspect(&RelPath::new(&request.input.project_name)?)? != FileSnapshot::Missing {
            return Err(
                YssError::new(ErrorCode::Conflict, "脚手架目标已存在，包括空目录")
                    .at(target.display()),
            );
        }
    } else if !request.dry_run {
        return Err(YssError::new(ErrorCode::Argument, "请先创建输出父目录"));
    }
    if !request.dry_run {
        fs::publish_new_tree(&target, &tree, cancel)?;
    }
    Ok(ScaffoldReport {
        root: target.display().to_string(),
        status: if request.dry_run {
            "planned"
        } else {
            "generated"
        },
        dry_run: request.dry_run,
        files: tree.files.len(),
        modules: modules(request.architecture),
        source: bundle.manifest.source.clone(),
    })
}
fn put(tree: &mut RenderedTree, path: &str, mut text: String) -> Result<()> {
    if !text.ends_with('\n') {
        text.push('\n');
    }
    tree.insert(RelPath::new(path)?, text.into_bytes(), 0o644)
}
fn template(bundle: &Bundle, path: &str, vars: &BTreeMap<String, String>) -> Result<String> {
    let bytes = bundle.read(Profile::Spec, path)?;
    let text = std::str::from_utf8(bytes)
        .map_err(|_| YssError::new(ErrorCode::InvalidInput, "模板不是 UTF-8"))?;
    context::replace(text, vars)
}
fn copy_wrapper(bundle: &Bundle, tree: &mut RenderedTree) -> Result<()> {
    let prefix = format!("{ASSETS}/wrapper/");
    for (path, file) in &bundle.profile(Profile::Spec)?.files {
        if let Some(path) = path.as_str().strip_prefix(&prefix) {
            tree.insert(
                RelPath::new(path)?,
                bundle.bytes(file)?.to_vec(),
                if path == "mvnw" {
                    0o755
                } else {
                    file.descriptor.mode
                },
            )?;
        }
    }
    if !tree.files.contains_key(&RelPath::new("mvnw")?) {
        return Err(YssError::new(ErrorCode::InvalidInput, "缺少 Maven Wrapper"));
    }
    Ok(())
}
fn local_database(name: &str) -> String {
    format!(
        "# 仅通过 scaffold-local 显式启用，不用于生产。\nspring:\n  datasource:\n    url: jdbc:h2:mem:{};DB_CLOSE_DELAY=-1\n    username: sa\n    password: \"\"\n    driver-class-name: org.h2.Driver\n  sql:\n    init:\n      mode: never\n  cloud:\n    discovery:\n      enabled: false\n    service-registry:\n      auto-registration:\n        enabled: false\n    nacos:\n      discovery:\n        enabled: false\n      config:\n        enabled: false\n  cache:\n    type: none\n",
        name.replace('-', "_")
    )
}
