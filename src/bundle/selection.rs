use super::{
    Bundle,
    manifest::{BundleFile, ProfileBundle},
};
use crate::{
    error::{ErrorCode, Result, YssError},
    model::{InstallationKind, Profile, RelPath},
};
use serde::Serialize;
use std::collections::{BTreeMap, BTreeSet};

#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Selection {
    pub defaults: BTreeSet<String>,
    pub explicit: BTreeSet<String>,
    pub closure: BTreeSet<String>,
    pub files: BTreeMap<RelPath, BundleFile>,
    pub resources: BTreeMap<String, BTreeSet<RelPath>>,
}
pub fn skill_for_path(path: &str) -> Option<&str> {
    let mut parts = path.split('/');
    if !matches!(
        parts.next()?,
        ".agents" | ".claude" | ".codex" | ".cursor" | ".pi" | ".workbuddy"
    ) || parts.next()? != "skills"
    {
        return None;
    }
    let name = parts.next()?;
    parts.next()?;
    Some(name)
}
impl ProfileBundle {
    pub fn skills(&self) -> BTreeSet<String> {
        self.files
            .keys()
            .filter_map(|p| {
                p.as_str()
                    .strip_prefix(".agents/skills/")?
                    .strip_suffix("/SKILL.md")
            })
            .filter(|s| !s.contains('/'))
            .map(str::to_owned)
            .collect()
    }
    pub fn closure(
        &self,
        kind: InstallationKind,
        selected: &BTreeSet<String>,
    ) -> Result<BTreeSet<String>> {
        let available = self.skills();
        let installation = self
            .installations
            .get(&kind)
            .ok_or_else(|| YssError::new(ErrorCode::InvalidInput, "缺少安装视图"))?;
        fn visit(
            name: &str,
            available: &BTreeSet<String>,
            dependencies: &BTreeMap<String, BTreeSet<String>>,
            active: &mut BTreeSet<String>,
            done: &mut BTreeSet<String>,
        ) -> Result<()> {
            if done.contains(name) {
                return Ok(());
            }
            if !available.contains(name) {
                return Err(
                    YssError::new(ErrorCode::InvalidInput, "清单引用不存在的技能").at(name),
                );
            }
            if !active.insert(name.into()) {
                return Err(YssError::new(ErrorCode::InvalidInput, "技能依赖存在环").at(name));
            }
            if let Some(deps) = dependencies.get(name) {
                for dep in deps {
                    visit(dep, available, dependencies, active, done)?;
                }
            }
            active.remove(name);
            done.insert(name.into());
            Ok(())
        }
        let mut closure = BTreeSet::new();
        let mut active = BTreeSet::new();
        for name in selected {
            visit(
                name,
                &available,
                &installation.skill_dependencies,
                &mut active,
                &mut closure,
            )?;
        }
        Ok(closure)
    }
}
impl Bundle {
    pub fn select(
        &self,
        profile: Profile,
        kind: InstallationKind,
        explicit: &BTreeSet<String>,
    ) -> Result<Selection> {
        let bundle = self.profile(profile)?;
        let mode = bundle
            .installations
            .get(&kind)
            .ok_or_else(|| YssError::new(ErrorCode::InvalidInput, "缺少安装视图"))?;
        let closure = bundle.closure(kind, &mode.defaults.union(explicit).cloned().collect())?;
        let mut files = mode.base.clone();
        let mut resources = BTreeMap::new();
        for name in &closure {
            for (path, file) in &bundle.files {
                if skill_for_path(path.as_str()) == Some(name) {
                    files.insert(path.clone(), file.clone());
                }
            }
            let refs = mode.skill_references.get(name).ok_or_else(|| {
                YssError::new(ErrorCode::InvalidInput, "技能缺少显式资源声明").at(name)
            })?;
            for path in refs {
                files
                    .entry(path.clone())
                    .or_insert_with(|| bundle.files[path].clone());
            }
            resources.insert(name.clone(), refs.clone());
        }
        Ok(Selection {
            defaults: mode.defaults.clone(),
            explicit: explicit.clone(),
            closure,
            files,
            resources,
        })
    }
}
