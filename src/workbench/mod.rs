pub mod content;
pub mod history;
pub mod lifecycle;
pub(crate) mod materials;
pub mod migration;
pub mod model;
pub mod preview;
pub mod progress;
pub mod progress_agent;
pub mod progress_tracking;
pub mod query;
mod read_model;
pub mod registration;
pub mod server;
pub mod service;
pub mod session;
pub mod sources;
pub(crate) mod sqlite;
pub(crate) mod status;
pub(crate) mod storage_migration;
pub mod store;
pub mod tasks;

pub(crate) mod activities;
pub mod api;
pub mod artifacts;
pub mod backup;
pub mod drafts;
pub mod jobs;

pub mod agent;
pub mod agent_protocol;
mod agent_scope;
pub mod agent_workspace;
mod ci_job;
#[cfg(all(target_os = "windows", target_arch = "x86_64"))]
#[allow(dead_code)]
mod command_recovery;
pub mod engineering;
mod grok;
pub mod integrations;
#[cfg(any(
    all(target_os = "macos", target_arch = "aarch64"),
    all(target_os = "windows", target_arch = "x86_64")
))]
pub mod managed_pi;
pub mod managed_tools;
mod merge_requests;
mod pnpm;
pub mod process;
