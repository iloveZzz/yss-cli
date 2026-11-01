use super::{ASSETS, ScaffoldInputs, local_database, put, template};
use crate::{
    bundle::Bundle,
    error::Result,
    model::{RelPath, RenderedTree},
};
use std::collections::BTreeMap;
pub(super) fn render(
    bundle: &Bundle,
    input: &ScaffoldInputs,
    vars: &BTreeMap<String, String>,
    tree: &mut RenderedTree,
) -> Result<()> {
    let name = &input.project_name;
    let package = input.base_package.replace('.', "/");
    for module in [
        "domain",
        "application",
        "infrastructure",
        "adapter",
        "bootstrap",
    ] {
        tree.directories.insert(RelPath::new(format!(
            "{name}-{module}/src/main/java/{package}"
        ))?);
        tree.directories
            .insert(RelPath::new(format!("{name}-{module}/src/main/resources"))?);
    }
    tree.directories.insert(RelPath::new(format!(
        "{name}-adapter/{name}-web/src/main/java/{package}/rest"
    ))?);
    tree.directories.insert(RelPath::new("db")?);
    for (source, target) in [
        ("parent", "pom.xml".into()),
        ("domain", format!("{name}-domain/pom.xml")),
        ("application", format!("{name}-application/pom.xml")),
        ("infrastructure", format!("{name}-infrastructure/pom.xml")),
        ("adapter", format!("{name}-adapter/pom.xml")),
        ("web", format!("{name}-adapter/{name}-web/pom.xml")),
        ("bootstrap", format!("{name}-bootstrap/pom.xml")),
    ] {
        put(
            tree,
            &target,
            template(
                bundle,
                &format!("{ASSETS}/templates/pom/{source}-pom.xml.template"),
                vars,
            )?,
        )?;
    }
    let main = format!(
        "{name}-bootstrap/src/main/java/{package}/{}.java",
        super::context::application_class(name)
    );
    put(
        tree,
        &main,
        template(
            bundle,
            &format!("{ASSETS}/templates/java/bootstrap-application.java.template"),
            vars,
        )?,
    )?;
    for name in ["application.yml", "logback-spring.xml"] {
        put(
            tree,
            &format!("{}-bootstrap/src/main/resources/{name}", input.project_name),
            template(
                bundle,
                &format!("{ASSETS}/templates/config/{name}.template"),
                vars,
            )?,
        )?;
    }
    put(
        tree,
        &format!("{name}-bootstrap/src/main/resources/application-scaffold-local.yml"),
        local_database(name),
    )
}
