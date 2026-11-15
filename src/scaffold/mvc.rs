use super::{ASSETS, ScaffoldInputs, local_database, put, template};
use crate::{bundle::Bundle, error::Result, model::RenderedTree};
use std::collections::BTreeMap;
pub(super) fn render(
    bundle: &Bundle,
    input: &ScaffoldInputs,
    vars: &BTreeMap<String, String>,
    tree: &mut RenderedTree,
) -> Result<()> {
    let name = &input.project_name;
    put(
        tree,
        "pom.xml",
        template(
            bundle,
            &format!("{ASSETS}/templates/pom/mvc-parent-pom.xml.template"),
            vars,
        )?,
    )?;
    for module in ["server", "service", "repository"] {
        put(
            tree,
            &format!("{name}-{module}/pom.xml"),
            template(
                bundle,
                &format!("{ASSETS}/templates/pom/mvc-{module}-pom.xml.template"),
                vars,
            )?,
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
