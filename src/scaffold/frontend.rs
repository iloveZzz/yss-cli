use super::context::validate_project_name;
use crate::{
    bundle::Bundle,
    cancel::Cancellation,
    codec::{self, Limits},
    error::{ErrorCode, Result, YssError},
    fs::{self, FileSnapshot, Root},
    model::{FileDescriptor, Profile, RelPath, RenderedTree, Sha256, SourceProvenance},
};
use serde::{Deserialize, Serialize};
use std::{
    collections::{BTreeMap, BTreeSet},
    path::PathBuf,
};

const PREFIX: &str = ".template-spec/templates/frontend-starter/";

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct FrontendInputs {
    pub project_name: String,
}
pub struct FrontendRequest {
    pub input: FrontendInputs,
    pub output_dir: PathBuf,
    pub dry_run: bool,
}
#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct FrontendReport {
    pub root: String,
    pub status: &'static str,
    pub dry_run: bool,
    pub files: usize,
    pub source: SourceProvenance,
    pub bundle_digest: Sha256,
}
#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct FrontendCatalog {
    pub command: &'static str,
    pub required_flags: Vec<&'static str>,
    pub engines: BTreeMap<String, String>,
    pub package_manager: String,
    pub source: SourceProvenance,
    pub bundle_digest: Sha256,
}
#[derive(Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct FrontendReceipt {
    format: String,
    format_version: u32,
    architecture: String,
    input: FrontendInputs,
    source: SourceProvenance,
    bundle_digest: Sha256,
    files: BTreeMap<RelPath, FileDescriptor>,
    directories: BTreeSet<RelPath>,
}
#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct Package {
    name: String,
    engines: BTreeMap<String, String>,
    package_manager: String,
}
fn package(bundle: &Bundle) -> Result<Package> {
    let package: Package = codec::decode_json(
        bundle.read(Profile::Frontend, &format!("{PREFIX}package.json"))?,
        Limits::manifest(),
    )?;
    if package.name.is_empty()
        || package.engines.get("node").is_none_or(String::is_empty)
        || package.package_manager.is_empty()
    {
        return Err(YssError::new(
            ErrorCode::InvalidInput,
            "前端资源缺少工程或运行环境声明",
        ));
    }
    Ok(package)
}
pub fn catalog(bundle: &Bundle) -> Result<FrontendCatalog> {
    let package = package(bundle)?;
    Ok(FrontendCatalog {
        command: "scaffold frontend",
        required_flags: vec!["project-name", "output-dir"],
        engines: package.engines,
        package_manager: package.package_manager,
        source: bundle.manifest.source.clone(),
        bundle_digest: bundle.digest.clone(),
    })
}
pub fn render(bundle: &Bundle, request: &FrontendRequest) -> Result<RenderedTree> {
    validate_project_name(&request.input.project_name)?;
    RelPath::new(&request.input.project_name)?;
    package(bundle)?;
    let mut tree = RenderedTree::default();
    for (path, file) in &bundle.profile(Profile::Frontend)?.files {
        if let Some(path) = path.as_str().strip_prefix(PREFIX) {
            let mut bytes = bundle.bytes(file)?.to_vec();
            if path == "package.json" {
                let mut value: serde_json::Value = codec::decode_json(&bytes, Limits::manifest())?;
                value["name"] = request.input.project_name.clone().into();
                bytes = codec::encode_json(&value)?;
            }
            tree.insert(RelPath::new(path)?, bytes, file.descriptor.mode)?;
        }
    }
    for required in [
        ".gitignore",
        "package.json",
        "pnpm-lock.yaml",
        "pnpm-workspace.yaml",
        "index.html",
        "vite.config.ts",
        "tsconfig.json",
        "eslint.config.mjs",
        "DESIGN.md",
        "README.md",
        "src/main.ts",
        "src/App.vue",
        "src/views/Home/index.vue",
        "src/styles/main.css",
        "src/styles/theme.css",
        "src/styles/tokens.css",
        "src/lib/utils.ts",
        "src/components/ui/button/Button.vue",
        "src/components/ui/button/index.ts",
        "src/components/ui/input/Input.vue",
        "src/components/ui/input/index.ts",
        "provenance/SHADCN-LICENSE.txt",
        "provenance/YSS-DESIGN-LICENSE.txt",
    ] {
        if !tree.files.contains_key(&RelPath::new(required)?) {
            return Err(
                YssError::new(ErrorCode::InvalidInput, "前端脚手架资源不完整").at(required),
            );
        }
    }
    for path in tree.files.keys() {
        for parent in path.parents() {
            tree.directories.insert(RelPath::new(parent)?);
        }
    }
    tree.directories.insert(RelPath::new(".yss")?);
    let receipt = FrontendReceipt {
        format: "yss.scaffold".into(),
        format_version: 1,
        architecture: "frontend".into(),
        input: request.input.clone(),
        source: bundle.manifest.source.clone(),
        bundle_digest: bundle.digest.clone(),
        files: tree
            .files
            .iter()
            .map(|(path, file)| {
                (
                    path.clone(),
                    FileDescriptor::new(&file.bytes, fs::observable_mode(file.mode)),
                )
            })
            .collect(),
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
    request: &FrontendRequest,
    cancel: &Cancellation,
) -> Result<FrontendReport> {
    cancel.check()?;
    let tree = render(bundle, request)?;
    let output = fs::absolute(&request.output_dir)?;
    let root = Root::open(&output)?;
    let name = RelPath::new(&request.input.project_name)?;
    let target = output.join(name.as_str());
    if root.inspect(&name)? != FileSnapshot::Missing {
        return Err(
            YssError::new(ErrorCode::Conflict, "脚手架目标已存在，包括空目录").at(target.display()),
        );
    }
    cancel.check()?;
    if !request.dry_run {
        fs::publish_new_tree(&target, &tree, cancel)?;
    }
    Ok(FrontendReport {
        root: target.display().to_string(),
        status: if request.dry_run {
            "planned"
        } else {
            "generated"
        },
        dry_run: request.dry_run,
        files: tree.files.len(),
        source: bundle.manifest.source.clone(),
        bundle_digest: bundle.digest.clone(),
    })
}
