use crate::{
    bundle::Bundle,
    codec::{self, Limits},
    error::{ErrorCode, Result, YssError},
    model::Profile,
};
use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;
use time::{OffsetDateTime, format_description::well_known::Rfc3339};

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct GenerationContext {
    pub author: String,
    pub date: String,
    pub generated_at: String,
}
impl GenerationContext {
    pub fn capture() -> Result<Self> {
        let now = OffsetDateTime::now_utc();
        let generated_at = now
            .format(&Rfc3339)
            .map_err(|_| invalid("无法格式化生成时间"))?;
        let author = std::env::var("USER")
            .or_else(|_| std::env::var("USERNAME"))
            .unwrap_or_else(|_| "yss-team".into());
        Ok(Self {
            author,
            date: now.date().to_string(),
            generated_at,
        })
    }
    pub fn validate(&self) -> Result<()> {
        if self.author.len() > 256
            || self.author.chars().any(char::is_control)
            || self.author.contains("*/")
            || self.author.contains('\\')
        {
            return Err(invalid("作者信息不合法"));
        }
        let parsed = OffsetDateTime::parse(&self.generated_at, &Rfc3339)
            .map_err(|_| invalid("生成时间不是 RFC3339"))?;
        if parsed.offset() != time::UtcOffset::UTC || parsed.date().to_string() != self.date {
            return Err(invalid("生成日期与 UTC 时间不一致"));
        }
        Ok(())
    }
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Coordinates {
    pub group_id: String,
    pub artifact_id: String,
    pub version: String,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Platform {
    pub id: String,
    pub spring_boot_version: String,
    pub java_version: u32,
    pub framework_line: String,
    pub tomcat_line: String,
    pub servlet_version: String,
    pub validation_namespace: String,
    pub validation_group: String,
    pub validation_artifact: String,
    pub web_starter: String,
    pub web_test_starter: String,
    pub aop_starter: String,
    pub mybatis_plus_starter: String,
    pub springdoc_major: u32,
    pub jackson_major: u32,
    pub swagger_artifact: String,
    pub versions: BTreeMap<String, String>,
    pub candidate_blockers: Vec<String>,
    #[serde(default)]
    pub component_platform_line: Option<String>,
    #[serde(default)]
    pub spring_cloud_line: Option<String>,
    #[serde(default)]
    pub openfeign_line: Option<String>,
    #[serde(default)]
    pub spring_cloud_version: Option<String>,
    #[serde(default)]
    pub spring_cloud_alibaba_version: Option<String>,
    #[serde(default)]
    pub auto_configuration_imports_path: Option<String>,
    #[serde(default)]
    pub spring_cloud_minimum: Option<String>,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub(super) struct CatalogInput {
    pub schema_version: u32,
    pub kind: String,
    pub profiles: Vec<Platform>,
    pub compatibility: Vec<Recipe>,
    #[serde(rename = "component_retirement_policy")]
    pub _component_retirement_policy: BTreeMap<String, serde_json::Value>,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub(super) struct Recipe {
    pub id: String,
    pub profile_id: String,
    pub spring_boot_version: String,
    pub parent: Coordinates,
    pub bom: Coordinates,
    pub status: String,
    #[serde(rename = "capabilities")]
    pub _capabilities: Vec<String>,
    #[serde(rename = "component_capabilities")]
    pub _component_capabilities: BTreeMap<String, serde_json::Value>,
    #[serde(rename = "evidence")]
    pub _evidence: Vec<serde_json::Value>,
    #[serde(default)]
    #[serde(rename = "artifact_resolution_evidence")]
    pub _artifact_resolution_evidence: BTreeMap<String, serde_json::Value>,
    #[serde(default)]
    #[serde(rename = "platform_artifact_bindings")]
    pub _platform_artifact_bindings: BTreeMap<String, serde_json::Value>,
    #[serde(default)]
    #[serde(rename = "external_snapshot_bindings")]
    pub _external_snapshot_bindings: Vec<serde_json::Value>,
    #[serde(default)]
    #[serde(rename = "external_artifact_bindings")]
    pub _external_artifact_bindings: Vec<serde_json::Value>,
    #[serde(default)]
    pub blockers: Vec<String>,
    #[serde(default)]
    #[serde(rename = "verification_scope")]
    pub _verification_scope: BTreeMap<String, serde_json::Value>,
    #[serde(default)]
    #[serde(rename = "components")]
    pub _components: BTreeMap<String, serde_json::Value>,
}
fn invalid(message: &str) -> YssError {
    YssError::new(ErrorCode::InvalidInput, message)
}
pub(super) fn catalog(bundle: &Bundle) -> Result<CatalogInput> {
    let input: CatalogInput = codec::decode_json(
        bundle.read(
            Profile::Spec,
            ".template-spec/engineering/backend-platforms.json",
        )?,
        Limits::manifest(),
    )?;
    if input.schema_version != 2 || input.kind != "backend-platform-catalog" {
        return Err(invalid("平台目录身份不合法"));
    }
    let mut keys = std::collections::BTreeSet::new();
    for p in &input.profiles {
        if !keys.insert(format!("{}@{}", p.id, p.spring_boot_version))
            || !exact_version(&p.spring_boot_version)
        {
            return Err(invalid("平台目录有重复或非精确版本"));
        }
        for value in [&p.web_starter, &p.aop_starter, &p.swagger_artifact] {
            if !coordinate(value) {
                return Err(invalid("平台依赖坐标不合法"));
            }
        }
        for key in [
            "mapstruct",
            "lombok",
            "lombok_mapstruct_binding",
            "swagger",
            "compiler",
        ] {
            if p.versions.get(key).is_none_or(|v| !coordinate(v)) {
                return Err(invalid("平台缺少生成所需版本"));
            }
        }
    }
    let mut keys = std::collections::BTreeSet::new();
    for recipe in &input.compatibility {
        if !keys.insert(&recipe.id) {
            return Err(invalid("平台配方 ID 重复"));
        }
    }
    Ok(input)
}
pub fn coordinate(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 256
        && value
            .bytes()
            .all(|c| c.is_ascii_alphanumeric() || matches!(c, b'_' | b'.' | b'-'))
}
pub fn exact_version(value: &str) -> bool {
    if !coordinate(value) {
        return false;
    }
    let (core, suffix) = value
        .split_once('-')
        .map_or((value, None), |(c, s)| (c, Some(s)));
    let parts: Vec<_> = core.split('.').collect();
    parts.len() == 3
        && parts
            .iter()
            .all(|p| !p.is_empty() && p.bytes().all(|c| c.is_ascii_digit()))
        && suffix.is_none_or(|s| !s.is_empty() && s.as_bytes()[0].is_ascii_alphanumeric())
}
pub(super) fn validate(bundle: &Bundle, input: &super::ScaffoldInputs) -> Result<Platform> {
    if input.project_name.len() > 128
        || !input.project_name.split('-').all(|p| {
            !p.is_empty()
                && p.bytes()
                    .all(|c| c.is_ascii_lowercase() || c.is_ascii_digit())
        })
        || !input
            .project_name
            .as_bytes()
            .first()
            .is_some_and(u8::is_ascii_lowercase)
    {
        return Err(invalid("工程名必须为 kebab-case"));
    }
    let reserved = [
        "class",
        "interface",
        "package",
        "public",
        "private",
        "protected",
        "abstract",
        "final",
        "static",
        "void",
        "int",
        "long",
        "short",
        "byte",
        "char",
        "float",
        "double",
        "boolean",
        "new",
        "return",
        "null",
        "true",
        "false",
        "if",
        "else",
        "switch",
        "case",
        "default",
        "try",
        "catch",
        "finally",
        "throws",
        "throw",
        "import",
        "this",
        "super",
        "extends",
        "implements",
        "enum",
        "assert",
        "synchronized",
        "volatile",
        "transient",
        "native",
        "strictfp",
        "const",
        "goto",
        "do",
        "while",
        "for",
        "break",
        "continue",
        "instanceof",
        "record",
        "sealed",
        "permits",
        "yield",
        "var",
    ];
    if input.base_package.len() > 256
        || !input.base_package.split('.').all(|p| {
            p.as_bytes().first().is_some_and(u8::is_ascii_lowercase)
                && p.bytes()
                    .all(|c| c.is_ascii_lowercase() || c.is_ascii_digit())
                && !reserved.contains(&p)
        })
    {
        return Err(invalid("Java 包名不合法"));
    }
    for value in [
        &input.group_id,
        &input.parent_group_id,
        &input.parent_artifact_id,
    ] {
        if !coordinate(value) {
            return Err(invalid("Maven 坐标不合法"));
        }
    }
    for value in [
        &input.project_version,
        &input.parent_version,
        &input.yss_components_version,
    ] {
        if !exact_version(value) {
            return Err(invalid("Maven 版本必须精确，不能包含范围或占位符"));
        }
    }
    let catalog = catalog(bundle)?;
    let platform = catalog
        .profiles
        .into_iter()
        .find(|p| {
            p.id == input.platform_profile && p.spring_boot_version == input.spring_boot_version
        })
        .ok_or_else(|| invalid("平台与 Spring Boot 精确版本不匹配"))?;
    if input.java_version != platform.java_version {
        return Err(invalid("Java 版本与所选平台不匹配"));
    }
    Ok(platform)
}
pub(super) fn variables(
    input: &super::ScaffoldInputs,
    platform: &Platform,
    context: &GenerationContext,
) -> BTreeMap<String, String> {
    let mut vars: BTreeMap<String, String> = platform
        .versions
        .iter()
        .map(|(k, v)| (format!("{k}_version"), v.clone()))
        .collect();
    for (key, value) in [
        ("project_name", input.project_name.clone()),
        ("base_package", input.base_package.clone()),
        (
            "application_class_name",
            application_class(&input.project_name),
        ),
        ("group_id", input.group_id.clone()),
        ("project_version", input.project_version.clone()),
        ("parent_group_id", input.parent_group_id.clone()),
        ("parent_artifact_id", input.parent_artifact_id.clone()),
        ("parent_version", input.parent_version.clone()),
        (
            "yss_components_version",
            input.yss_components_version.clone(),
        ),
        (
            "project_description",
            format!("{} service", input.project_name),
        ),
        ("author", context.author.clone()),
        ("date", context.date.clone()),
        ("boot_version", platform.spring_boot_version.clone()),
        (
            "java_version",
            if platform.java_version == 8 {
                "1.8".into()
            } else {
                platform.java_version.to_string()
            },
        ),
        (
            "java_range",
            if platform.java_version == 8 {
                "[1.8,1.9)".into()
            } else {
                format!("[{},{})", platform.java_version, platform.java_version + 1)
            },
        ),
        ("web_starter", platform.web_starter.clone()),
        ("aop_starter", platform.aop_starter.clone()),
        ("swagger_artifact", platform.swagger_artifact.clone()),
        ("db_dependency", String::new()),
    ] {
        vars.insert(key.into(), value);
    }
    vars
}
pub(super) fn application_class(name: &str) -> String {
    format!(
        "{}Application",
        name.split('-')
            .map(|s| format!("{}{}", s[..1].to_ascii_uppercase(), &s[1..]))
            .collect::<String>()
    )
}
pub(super) fn replace(template: &str, vars: &BTreeMap<String, String>) -> Result<String> {
    let mut out = String::new();
    let mut remaining = template;
    while let Some(start) = remaining.find("{{") {
        out.push_str(&remaining[..start]);
        remaining = &remaining[start + 2..];
        let end = remaining
            .find("}}")
            .ok_or_else(|| invalid("模板占位符未闭合"))?;
        out.push_str(
            vars.get(&remaining[..end])
                .ok_or_else(|| invalid("模板包含未定义占位符"))?,
        );
        remaining = &remaining[end + 2..];
    }
    out.push_str(remaining);
    Ok(out)
}
