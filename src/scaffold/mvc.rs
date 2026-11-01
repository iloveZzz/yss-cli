use super::{ASSETS, ScaffoldInputs, context::Platform, local_database, put, template};
use crate::{bundle::Bundle, error::Result, model::RenderedTree};
use std::collections::BTreeMap;
fn dep(group: &str, artifact: &str, version: &str, scope: &str) -> String {
    let mut xml =
        format!("<dependency><groupId>{group}</groupId><artifactId>{artifact}</artifactId>");
    if !version.is_empty() {
        xml.push_str(&format!("<version>{version}</version>"));
    }
    if !scope.is_empty() {
        xml.push_str(&format!("<scope>{scope}</scope>"));
    }
    xml.push_str("</dependency>");
    xml
}
pub(super) fn render(
    bundle: &Bundle,
    input: &ScaffoldInputs,
    platform: &Platform,
    vars: &BTreeMap<String, String>,
    tree: &mut RenderedTree,
) -> Result<()> {
    let name = &input.project_name;
    let group = &input.group_id;
    let version = &input.project_version;
    let modules = ["server", "service", "repository"];
    let module_xml = modules
        .iter()
        .map(|m| format!("<module>{name}-{m}</module>"))
        .collect::<String>();
    let parent = format!(
        r#"<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <parent><groupId>{}</groupId><artifactId>{}</artifactId><version>{}</version><relativePath/></parent>
  <groupId>{group}</groupId><artifactId>{name}</artifactId><version>{version}</version><packaging>pom</packaging>
  <properties><java.version>{{{{java_version}}}}</java.version><spring-boot.version>{{{{boot_version}}}}</spring-boot.version><maven.compiler.source>{{{{java_version}}}}</maven.compiler.source><maven.compiler.target>{{{{java_version}}}}</maven.compiler.target><project.build.sourceEncoding>UTF-8</project.build.sourceEncoding><yss-components.version>{}</yss-components.version></properties>
  <modules>{module_xml}</modules>
  <dependencyManagement><dependencies><dependency><groupId>org.springframework.boot</groupId><artifactId>spring-boot-dependencies</artifactId><version>{{{{boot_version}}}}</version><scope>import</scope><type>pom</type></dependency><dependency><groupId>com.yss.cloud</groupId><artifactId>yss-components-bom</artifactId><version>${{yss-components.version}}</version><scope>import</scope><type>pom</type></dependency></dependencies></dependencyManagement>
  <build><plugins><plugin><groupId>org.springframework.boot</groupId><artifactId>spring-boot-maven-plugin</artifactId><version>{{{{boot_version}}}}</version><configuration><skip>true</skip></configuration></plugin></plugins><pluginManagement><plugins><plugin><groupId>org.springframework.boot</groupId><artifactId>spring-boot-maven-plugin</artifactId><version>{{{{boot_version}}}}</version><executions><execution><goals><goal>repackage</goal></goals></execution></executions></plugin><plugin><groupId>org.apache.maven.plugins</groupId><artifactId>maven-compiler-plugin</artifactId><version>{{{{compiler_version}}}}</version><configuration><source>{{{{java_version}}}}</source><target>{{{{java_version}}}}</target><parameters>true</parameters><annotationProcessorPaths><path><groupId>org.projectlombok</groupId><artifactId>lombok</artifactId><version>{{{{lombok_version}}}}</version></path><path><groupId>org.mapstruct</groupId><artifactId>mapstruct-processor</artifactId><version>{{{{mapstruct_version}}}}</version></path><path><groupId>org.projectlombok</groupId><artifactId>lombok-mapstruct-binding</artifactId><version>{{{{lombok_mapstruct_binding_version}}}}</version></path></annotationProcessorPaths></configuration></plugin></plugins></pluginManagement></build>
</project>"#,
        input.parent_group_id,
        input.parent_artifact_id,
        input.parent_version,
        input.yss_components_version
    );
    put(tree, "pom.xml", super::context::replace(&parent, vars)?)?;
    for module in modules {
        let own = |m: &str| dep(group, &format!("{name}-{m}"), "${project.version}", "");
        let mut dependencies = match module {
            "server" => vec![
                own("service"),
                dep("org.springframework.boot", &platform.web_starter, "", ""),
                dep(
                    "org.springframework.boot",
                    "spring-boot-starter-validation",
                    "",
                    "",
                ),
                dep(
                    "org.mapstruct",
                    "mapstruct",
                    &platform.versions["mapstruct"],
                    "",
                ),
                dep("com.yss.cloud", "yss-component-dto", "", ""),
            ],
            "service" => vec![
                own("repository"),
                dep("org.springframework", "spring-tx", "", ""),
                dep("com.yss.cloud", "yss-component-dto", "", ""),
            ],
            _ => vec![
                dep(
                    "com.yss.cloud",
                    "yss-component-mybatis-plus-starter",
                    "",
                    "",
                ),
                dep(
                    "org.mapstruct",
                    "mapstruct",
                    &platform.versions["mapstruct"],
                    "",
                ),
            ],
        };
        dependencies.push(dep(
            "org.projectlombok",
            "lombok",
            &platform.versions["lombok"],
            "provided",
        ));
        let plugin = if module == "server" {
            format!(
                "<profiles><profile><id>scaffold-local</id><dependencies>{}</dependencies></profile></profiles><build><plugins><plugin><groupId>org.springframework.boot</groupId><artifactId>spring-boot-maven-plugin</artifactId><configuration><skip>false</skip></configuration></plugin></plugins></build>",
                dep("com.h2database", "h2", "", "runtime")
            )
        } else {
            String::new()
        };
        put(
            tree,
            &format!("{name}-{module}/pom.xml"),
            format!(
                "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<project xmlns=\"http://maven.apache.org/POM/4.0.0\"><modelVersion>4.0.0</modelVersion><parent><groupId>{group}</groupId><artifactId>{name}</artifactId><version>{version}</version></parent><artifactId>{name}-{module}</artifactId><dependencies>{}</dependencies>{plugin}</project>",
                dependencies.join("")
            ),
        )?;
        put(
            tree,
            &format!(
                "{name}-{module}/src/main/java/{}/{module}/package-info.java",
                input.base_package.replace('.', "/")
            ),
            format!(
                "/** {module} package boundary. */\npackage {}.{module};",
                input.base_package
            ),
        )?;
    }
    put(
        tree,
        &format!(
            "{name}-server/src/main/java/{}/{}.java",
            input.base_package.replace('.', "/"),
            super::context::application_class(name)
        ),
        template(
            bundle,
            &format!("{ASSETS}/templates/java/bootstrap-application.java.template"),
            vars,
        )?,
    )?;
    put(
        tree,
        &format!("{name}-server/src/main/resources/application.yml"),
        format!("spring:\n  application:\n    name: {name}"),
    )?;
    put(
        tree,
        &format!("{name}-server/src/main/resources/application-scaffold-local.yml"),
        local_database(name),
    )
}
