use super::{
    jobs::JobRecord,
    materials,
    model::*,
    progress::{self, ProgressBasis, ProgressSummary, ProjectVersion},
    progress_agent::RunActivity,
    query, service, sources,
    sqlite::ExtensionRecords,
    store::Store,
};
use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
    model::Sha256,
};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use std::{
    cmp::Reverse,
    collections::{BTreeMap, BTreeSet},
    sync::{
        Arc, Mutex,
        atomic::{AtomicU64, Ordering},
    },
};
use time::{Date, OffsetDateTime, UtcOffset, format_description::well_known::Rfc3339};
use tokio::sync::{Notify, watch};

static NEXT_OBSERVATION_ID: AtomicU64 = AtomicU64::new(1);

pub(super) struct ReadInputs {
    pub(super) stamp: (u64, u64),
    observation_id: u64,
    state: WorkbenchState,
    records: ExtensionRecords,
}

pub(super) fn capture(store: &mut Store) -> Result<ReadInputs> {
    let records = store.capture_read_records(&service::record_kinds())?;
    Ok(ReadInputs {
        stamp: store.projection_stamp(),
        observation_id: NEXT_OBSERVATION_ID.fetch_add(1, Ordering::Relaxed),
        state: store.state().clone(),
        records,
    })
}

pub(super) fn build_snapshot(
    inputs: ReadInputs,
    spec_id: Option<&str>,
    cancel: &Cancellation,
) -> Result<Snapshot> {
    let materials = materials::Records::from_raw(&inputs.records)?;
    let mut records = progress::ProgressRecords::from_raw(&inputs.records)?;
    if let Some(id) = spec_id {
        service::scope_progress(&mut records, id);
    }
    service::build_snapshot(&inputs.state, records, &materials, spec_id, cancel)
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub(super) struct SpecDetail {
    snapshot: Snapshot,
    spec_version: String,
    observation_id: u64,
}

pub(super) fn read_spec_detail(
    store: &Arc<Mutex<Store>>,
    reads: &ReadModel,
    id: &str,
    fresh: bool,
    cancel: &Cancellation,
) -> Result<SpecDetail> {
    let mut store = super::jobs::lock_store(store)?;
    let record = sources::spec(store.state(), id)?;
    let project = sources::project(store.state(), &record.project_id)?;
    if !fresh {
        let published = reads.latest();
        if let Some(projection) = published.projection.as_deref()
            && let Some(spec_version) = projection.spec_versions.get(id)
            && let Some(spec) = projection.snapshot.specs.iter().find(|spec| {
                spec.record.id == record.id
                    && spec.record.project_id == record.project_id
                    && spec.record.source_path == record.source_path
            })
            && projection.snapshot.projects.iter().any(|observed| {
                observed.record.id == project.id && observed.record.root_path == project.root_path
            })
        {
            store.capture_read_records(&[])?;
            drop(store);
            return Ok(projection.spec_detail(spec, spec_version));
        }
    }
    let inputs = capture(&mut store)?;
    drop(store);

    let observation_id = inputs.observation_id;
    let mut snapshot = build_snapshot(inputs, Some(id), cancel)?;
    snapshot.history.clear();
    snapshot.stage_records.retain(|record| record.spec_id == id);
    let spec = snapshot
        .specs
        .first()
        .ok_or_else(|| sources::missing("Spec 不存在"))?;
    let project = snapshot
        .projects
        .iter()
        .find(|project| project.record.id == spec.record.project_id);
    let source_version = fingerprint_source(spec)?;
    let spec_version = fingerprint_spec(
        spec,
        project.map(|project| &project.record),
        &source_version,
        &snapshot.tasks.iter().collect::<Vec<_>>(),
        &snapshot.stage_records.iter().collect::<Vec<_>>(),
    )?;
    Ok(SpecDetail {
        snapshot,
        spec_version,
        observation_id,
    })
}

pub(super) struct Projection {
    pub(super) stamp: (u64, u64),
    observation_id: u64,
    pub(super) snapshot: Snapshot,
    pub(super) next_review_at: Option<OffsetDateTime>,
    version: String,
    source_versions: BTreeMap<String, String>,
    spec_versions: BTreeMap<String, String>,
    input_versions: String,
    overview: Overview,
}

impl Projection {
    fn spec_detail(&self, spec: &SpecView, spec_version: &str) -> SpecDetail {
        let snapshot = &self.snapshot;
        let id = &spec.record.id;
        SpecDetail {
            snapshot: Snapshot {
                progress_records: progress::ProgressRecords::default(),
                progress: spec
                    .progress
                    .iter()
                    .map(|progress| (id.clone(), progress.clone()))
                    .collect(),
                revision: snapshot.revision,
                read_at: snapshot.read_at.clone(),
                projects: snapshot.projects.clone(),
                specs: vec![spec.clone()],
                history: Vec::new(),
                tasks: snapshot
                    .tasks
                    .iter()
                    .filter(|task| &task.record.spec_id == id)
                    .cloned()
                    .collect(),
                stage_records: snapshot
                    .stage_records
                    .iter()
                    .filter(|record| &record.spec_id == id)
                    .cloned()
                    .collect(),
                saved_views: snapshot.saved_views.clone(),
            },
            spec_version: spec_version.to_owned(),
            observation_id: self.observation_id,
        }
    }
}

pub(super) fn fingerprint(value: &impl Serialize) -> Result<String> {
    Ok(Sha256::of(&crate::codec::encode_json(value)?)
        .as_str()
        .to_owned())
}

fn fingerprint_source(spec: &SpecView) -> Result<String> {
    fingerprint(&(
        &spec.source_version,
        spec.source_health,
        &spec.source_modified_at,
        &spec.resources,
        &spec.content,
    ))
}

fn fingerprint_spec(
    spec: &SpecView,
    project: Option<&ProjectRecord>,
    source_version: &str,
    tasks: &[&TaskView],
    stage_records: &[&StageRecord],
) -> Result<String> {
    fingerprint(&(
        &spec.record,
        project,
        source_version,
        &spec.progress,
        tasks,
        stage_records,
    ))
}

pub(super) fn build(inputs: ReadInputs, cancel: &Cancellation) -> Result<Projection> {
    let stamp = inputs.stamp;
    let observation_id = inputs.observation_id;
    let snapshot = build_snapshot(inputs, None, cancel)?;
    let next_review_at = next_review_at(&snapshot);
    let input_versions = progress::input_versions_digest(&snapshot.progress_records);
    let projects: BTreeMap<_, _> = snapshot
        .projects
        .iter()
        .map(|project| (&project.record.id, project))
        .collect();
    let mut tasks = BTreeMap::<&str, Vec<&TaskView>>::new();
    for task in &snapshot.tasks {
        tasks.entry(&task.record.spec_id).or_default().push(task);
    }
    let mut stage_records = BTreeMap::<&str, Vec<&StageRecord>>::new();
    for record in &snapshot.stage_records {
        stage_records
            .entry(&record.spec_id)
            .or_default()
            .push(record);
    }
    let mut source_versions = BTreeMap::new();
    let mut spec_versions = BTreeMap::new();
    for spec in &snapshot.specs {
        cancel.check()?;
        let id = spec.record.id.as_str();
        let source_version = fingerprint_source(spec)?;
        let spec_version = fingerprint_spec(
            spec,
            projects
                .get(&spec.record.project_id)
                .map(|project| &project.record),
            &source_version,
            tasks.get(id).map_or(&[], Vec::as_slice),
            stage_records.get(id).map_or(&[], Vec::as_slice),
        )?;
        source_versions.insert(spec.record.id.clone(), source_version);
        spec_versions.insert(spec.record.id.clone(), spec_version);
    }
    let version = fingerprint(&(
        stamp,
        &spec_versions,
        &snapshot.projects,
        &snapshot.tasks,
        &snapshot.progress_records.current_versions,
        &input_versions,
    ))?;
    let overview = overview(&snapshot)?;
    Ok(Projection {
        stamp,
        observation_id,
        snapshot,
        version,
        source_versions,
        spec_versions,
        input_versions,
        overview,
        next_review_at,
    })
}

fn next_review_at(snapshot: &Snapshot) -> Option<OffsetDateTime> {
    let now = OffsetDateTime::now_utc();
    let format = time::format_description::parse_borrowed::<2>("[year]-[month]-[day]")
        .expect("review date format");
    snapshot
        .specs
        .iter()
        .flat_map(|spec| &spec.content.questions)
        .filter_map(|question| question.response.as_ref())
        .filter(|response| response.kind == ResponseKind::Defer)
        .filter_map(|response| {
            let zone =
                UtcOffset::from_whole_seconds(i32::from(response.review_utc_offset_minutes?) * 60)
                    .ok()?;
            let date = Date::parse(&response.review_at, &format).ok()?;
            Some(date.midnight().assume_offset(zone))
        })
        .filter(|date| *date > now)
        .min()
}

#[derive(Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
enum Verification {
    Pending,
    Current,
    Stale,
    Failed,
}

#[derive(Clone)]
pub(super) struct ReadStatus {
    sequence: u64,
    revision: u64,
    projection: Option<Arc<Projection>>,
    verification: Verification,
    verification_error: Option<String>,
    activity: Arc<ActivitySummary>,
    agent_contexts: BTreeMap<String, Arc<ScopedAgentContext>>,
}

struct ScopedAgentContext {
    stamp: (u64, u64),
    value: Value,
    version: String,
}

#[derive(Default)]
struct ActivitySummary {
    versions: BTreeMap<String, String>,
    agent_session_versions: BTreeMap<String, String>,
    agent_queue_versions: BTreeMap<String, String>,
    runs: BTreeMap<String, Vec<RunActivity>>,
    external_version: String,
    jobs: Vec<JobSummary>,
    jobs_version: String,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct JobSummary {
    id: String,
    title: String,
    status: String,
    roots: Vec<String>,
    updated_at: String,
}

pub(super) struct ReadModel {
    changes: watch::Sender<Arc<ReadStatus>>,
    refresh: Notify,
    pending_agent_contexts: Mutex<BTreeSet<(String, (u64, u64))>>,
}

impl ReadModel {
    pub(super) fn new() -> Self {
        let (changes, _) = watch::channel(Arc::new(ReadStatus {
            sequence: 0,
            revision: 0,
            projection: None,
            verification: Verification::Pending,
            verification_error: None,
            activity: Arc::new(ActivitySummary::default()),
            agent_contexts: BTreeMap::new(),
        }));
        Self {
            changes,
            refresh: Notify::new(),
            pending_agent_contexts: Mutex::default(),
        }
    }

    pub(super) fn latest(&self) -> Arc<ReadStatus> {
        Arc::clone(&self.changes.borrow())
    }

    pub(super) fn subscribe(&self) -> watch::Receiver<Arc<ReadStatus>> {
        self.changes.subscribe()
    }

    pub(super) fn request_refresh(&self) {
        self.refresh.notify_one();
    }

    pub(super) async fn refresh_requested(&self) {
        self.refresh.notified().await;
    }

    pub(super) fn mark_pending(&self, revision: u64) {
        self.changes.send_if_modified(|current| {
            let next = if current.projection.is_some() {
                Verification::Stale
            } else {
                Verification::Pending
            };
            if current.revision == revision && current.verification == next {
                return false;
            }
            let current = Arc::make_mut(current);
            current.sequence += 1;
            current.revision = revision;
            current.verification = next;
            current.verification_error = None;
            true
        });
    }

    pub(super) fn mark_failed(&self, revision: u64, error: &YssError) {
        self.changes.send_if_modified(|current| {
            if current.revision == revision
                && current.verification == Verification::Failed
                && current.verification_error.as_deref() == Some(error.message.as_str())
            {
                return false;
            }
            let current = Arc::make_mut(current);
            current.sequence += 1;
            current.revision = revision;
            current.verification = Verification::Failed;
            current.verification_error = Some(error.message.clone());
            true
        });
    }

    pub(super) fn publish(&self, projection: Projection) {
        // A scoped read may have observed an intermediate file change between identical projections.
        self.changes.send_modify(|current| {
            let current = Arc::make_mut(current);
            current.sequence += 1;
            current.revision = projection.snapshot.revision;
            current.projection = Some(Arc::new(projection));
            current.agent_contexts.clear();
            current.verification = Verification::Current;
            current.verification_error = None;
        });
    }

    pub(super) fn request_agent_context(
        self: &Arc<Self>,
        store: &Arc<Mutex<Store>>,
        id: &str,
        stamp: (u64, u64),
        cancel: &Cancellation,
    ) {
        let current = self.latest();
        if current.projection.is_some()
            || current
                .agent_contexts
                .get(id)
                .is_some_and(|value| value.stamp == stamp)
        {
            return;
        }
        let Ok(runtime) = tokio::runtime::Handle::try_current() else {
            return;
        };
        let key = (id.to_owned(), stamp);
        let Ok(mut pending) = self.pending_agent_contexts.lock() else {
            return;
        };
        if pending.len() >= 4 || !pending.insert(key.clone()) {
            return;
        }
        let (reads, store, cancel) = (self.clone(), store.clone(), cancel.clone());
        runtime.spawn_blocking(move || {
            let (id, stamp) = &key;
            let result = (|| -> Result<Option<Snapshot>> {
                let inputs = {
                    let mut store = super::jobs::lock_store(&store)?;
                    if store.projection_stamp() != *stamp {
                        return Ok(None);
                    }
                    capture(&mut store)?
                };
                build_snapshot(inputs, Some(id), &cancel).map(Some)
            })();
            if let Ok(store) = super::jobs::lock_store(&store)
                && store.projection_stamp() == *stamp
                && !cancel.is_cancelled()
            {
                let value = match result {
                    Ok(Some(snapshot)) => Some(agent_context_value(
                        snapshot.specs.iter().find(|spec| &spec.record.id == id),
                        Verification::Current,
                        None,
                    )),
                    Ok(None) => None,
                    Err(error) => Some(agent_context_value(
                        None,
                        Verification::Failed,
                        Some(&error.message),
                    )),
                };
                if let Some(value) = value
                    && let Ok(version) = fingerprint(&(stamp, &value))
                {
                    reads.changes.send_if_modified(|current| {
                        if current.projection.is_some() {
                            return false;
                        }
                        let current = Arc::make_mut(current);
                        current.sequence += 1;
                        current.agent_contexts.insert(
                            id.clone(),
                            Arc::new(ScopedAgentContext {
                                stamp: *stamp,
                                value,
                                version,
                            }),
                        );
                        true
                    });
                }
            }
            if let Ok(mut pending) = reads.pending_agent_contexts.lock() {
                pending.remove(&key);
            }
        });
    }

    pub(super) fn publish_activity(
        &self,
        activities: BTreeMap<String, Vec<RunActivity>>,
        mut jobs: Vec<JobRecord>,
        external_version: String,
        agent_session_versions: BTreeMap<String, String>,
        agent_queue_versions: BTreeMap<String, String>,
    ) -> Result<()> {
        let versions = activities
            .iter()
            .map(|(id, activities)| Ok((id.clone(), fingerprint(activities)?)))
            .collect::<Result<BTreeMap<_, _>>>()?;
        fn priority(status: &str) -> u8 {
            match status {
                "waitingInput" => 0,
                "recoveryRequired" => 1,
                "failed" => 3,
                _ => 2,
            }
        }
        jobs.retain(|job| {
            !job.terminal() || matches!(job.status.as_str(), "failed" | "recoveryRequired")
        });
        jobs.sort_by(|left, right| {
            priority(&left.status)
                .cmp(&priority(&right.status))
                .then_with(|| right.updated_at.cmp(&left.updated_at))
                .then_with(|| left.id.cmp(&right.id))
        });
        let jobs: Vec<_> = jobs
            .into_iter()
            .take(6)
            .map(|job| JobSummary {
                id: job.id,
                title: job.title,
                status: job.status,
                roots: job.roots,
                updated_at: job.updated_at,
            })
            .collect();
        let jobs_version = fingerprint(&jobs)?;
        self.changes.send_if_modified(|current| {
            if current.activity.versions == versions
                && current.activity.agent_session_versions == agent_session_versions
                && current.activity.agent_queue_versions == agent_queue_versions
                && current.activity.external_version == external_version
                && current.activity.jobs_version == jobs_version
            {
                return false;
            }
            let current = Arc::make_mut(current);
            current.sequence += 1;
            current.activity = Arc::new(ActivitySummary {
                versions,
                agent_session_versions,
                agent_queue_versions,
                runs: activities,
                external_version,
                jobs,
                jobs_version,
            });
            true
        });
        Ok(())
    }
}

impl ReadStatus {
    pub(super) fn change_state(&self) -> Value {
        let projection = self.projection.as_ref();
        let progress_versions: &[ProjectVersion] = projection
            .map(|view| view.snapshot.progress_records.current_versions.as_slice())
            .unwrap_or_default();
        json!({
            "changeSequence": self.sequence,
            "revision": self.revision,
            "sourceVersions": projection.map(|view| json!(view.source_versions)).unwrap_or_else(|| json!({})),
            "specVersions": projection.map(|view| json!(view.spec_versions)).unwrap_or_else(|| {
                json!(self.agent_contexts.iter().map(|(id, value)| (id, &value.version)).collect::<BTreeMap<_, _>>())
            }),
            "progressVersions": progress_versions,
            "inputVersions": projection.map(|view| view.input_versions.as_str()).unwrap_or_default(),
            "externalVersion": self.activity.external_version,
            "activityVersions": self.activity.versions,
            "agentSessionVersions": self.activity.agent_session_versions,
            "agentQueueVersions": self.activity.agent_queue_versions,
            "jobsVersion": self.activity.jobs_version,
            "projectionVersion": projection.map(|view| &view.version),
            "projectionRevision": projection.map(|view| view.snapshot.revision),
            "projectionObservationId": projection.map(|view| view.observation_id),
            "verification": self.verification,
            "verificationError": self.verification_error,
            "nextReviewAt": projection.and_then(|view| view.next_review_at).and_then(|at| at.format(&Rfc3339).ok()),
            "readAt": projection.map_or_else(sources::now, |view| view.snapshot.read_at.clone()),
        })
    }

    pub(super) fn agent_context(&self, id: &str, stamp: (u64, u64)) -> Value {
        if let Some(scoped) = self
            .agent_contexts
            .get(id)
            .filter(|value| value.stamp == stamp)
        {
            return scoped.value.clone();
        }
        let projection = self.projection.as_deref();
        let current = self.verification == Verification::Current
            && projection.is_some_and(|view| view.stamp == stamp);
        let verification = if self.verification == Verification::Current && !current {
            Verification::Stale
        } else {
            self.verification
        };
        let spec = projection
            .and_then(|view| view.snapshot.specs.iter().find(|spec| spec.record.id == id));
        agent_context_value(spec, verification, self.verification_error.as_deref())
    }
    pub(super) fn overview(&self, base: OverviewBase) -> Value {
        let projection = self.projection.as_ref();
        let empty = Overview::default();
        let overview = projection.map_or(&empty, |view| &view.overview);
        let verification = if self.verification == Verification::Current
            && projection.is_some_and(|view| view.stamp != base.stamp)
        {
            Verification::Stale
        } else {
            self.verification
        };
        json!({
            "changeSequence": self.sequence,
            "revision": projection.map_or(base.stamp.0, |view| view.snapshot.revision),
            "readAt": projection.map_or_else(sources::now, |view| view.snapshot.read_at.clone()),
            "projectionVersion": projection.map(|view| &view.version),
            "verification": verification,
            "verificationError": self.verification_error,
            "nextReviewAt": projection
                .and_then(|view| view.next_review_at)
                .and_then(|at| at.format(&Rfc3339).ok()),
            "groups": overview.groups,
            "reminders": overview.reminders,
            "recent": base.recent,
            "activity": base.activity,
            "activeSpecCount": base.active_spec_count,
            "projectCount": base.project_count,
            "jobs": self.activity.jobs,
        })
    }

    pub(super) fn jobs(&self) -> Value {
        json!({"items": self.activity.jobs, "version": self.activity.jobs_version})
    }

    pub(super) fn run_activities(
        &self,
        spec_id: &str,
        goal_version: Option<&str>,
        spec_digest: Option<&str>,
    ) -> Vec<&RunActivity> {
        self.activity
            .runs
            .get(spec_id)
            .into_iter()
            .flatten()
            .filter(|run| {
                goal_version.is_none_or(|goal| run.goal_version == goal)
                    && spec_digest.is_none_or(|digest| run.spec_digest == digest)
            })
            .collect()
    }

    pub(super) fn work_items(
        &self,
        query: &WorkQuery,
        offset: usize,
        limit: usize,
        expected_version: Option<&str>,
        expected_query_version: Option<&str>,
        board: Option<&BoardRequest>,
    ) -> Result<Value> {
        let query = query::normalize_query(query.clone())?;
        let projection = self.projection.as_ref();
        let version = projection.map(|view| view.version.as_str());
        let query_version = fingerprint(&(&query, board.map(|value| value.group_by)))?;
        if expected_version.is_some_and(|expected| Some(expected) != version)
            || expected_query_version.is_some_and(|expected| expected != query_version)
        {
            return Err(YssError::new(
                ErrorCode::ConcurrentChange,
                "规格查询结果已变化，请从第一页重新读取",
            )
            .context("projectionVersion", version.unwrap_or_default())
            .context("queryVersion", &query_version));
        }
        let mut page = QueryPage::new(board)?;
        if let Some(view) = projection {
            let result = query::filter_items(&view.snapshot, &query)?;
            page.total = result.total;
            for item in result.items {
                if page.includes(&item, offset, limit) {
                    page.items.push(compact_item(&view.snapshot, item)?);
                }
            }
        }
        let mut response = json!({
            "changeSequence": self.sequence,
            "revision": projection.map_or(self.revision, |view| view.snapshot.revision),
            "readAt": projection.map_or_else(sources::now, |view| view.snapshot.read_at.clone()),
            "projectionVersion": version,
            "queryVersion": query_version,
            "verification": self.verification,
            "verificationError": self.verification_error,
            "nextReviewAt": projection.and_then(|view| view.next_review_at).and_then(|at| at.format(&Rfc3339).ok()),
            "coverage": projection.map(|view| coverage(&view.snapshot, &query)).unwrap_or_default(),
            "total": page.total,
            "items": page.items,
        });
        if board.is_some() {
            response["columnTotals"] = json!(page.column_totals);
        }
        Ok(response)
    }
}

fn agent_context_value(
    spec: Option<&SpecView>,
    verification: Verification,
    verification_error: Option<&str>,
) -> Value {
    let stage_binding = spec
        .filter(|_| verification == Verification::Current)
        .and_then(|spec| {
            let progress = spec.progress.as_ref()?;
            let goal = progress.goal.as_ref()?;
            let digest = spec.content.artifact_digest.as_deref()?;
            if goal.goal.spec_digest != digest {
                return None;
            }
            let stages: Vec<_> = goal
                .goal
                .stages
                .iter()
                .filter(|stage| stage.applicable)
                .map(|stage| stage.stage)
                .collect();
            let primary_stage = (progress.status != Status::Done
                && stages.contains(&progress.primary_stage))
            .then_some(progress.primary_stage);
            Some(json!({
                "goalVersion": goal.goal_version,
                "specDigest": digest,
                "stages": stages,
                "primaryStage": primary_stage,
            }))
        });
    json!({
        "verification": verification,
        "verificationError": verification_error,
        "sourceError": spec.and_then(|spec| spec.source_message.as_deref()),
        "stageBinding": stage_binding,
    })
}

#[derive(Default, Serialize)]
struct Coverage {
    total: usize,
    inactive: usize,
    closed: usize,
}

fn coverage(view: &Snapshot, query: &WorkQuery) -> Coverage {
    let active: std::collections::BTreeSet<_> = view
        .projects
        .iter()
        .filter(|project| project.record.active)
        .map(|project| &project.record.id)
        .collect();
    let mut coverage = Coverage::default();
    for spec in &view.specs {
        let record = &spec.record;
        if (!query.project_ids.is_empty() && !query.project_ids.contains(&record.project_id))
            || (!query.spec_ids.is_empty() && !query.spec_ids.contains(&record.id))
        {
            continue;
        }
        coverage.total += 1;
        if !record.active || !active.contains(&record.project_id) {
            coverage.inactive += 1;
            continue;
        }
        let status = if query.progress_basis == ProgressBasis::Effective {
            view.progress
                .get(&record.id)
                .map_or(Status::Unmarked, |progress| progress.status)
        } else {
            record.status
        };
        coverage.closed += usize::from(matches!(status, Status::Done | Status::Canceled));
    }
    coverage
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub(super) struct Catalog {
    revision: u64,
    read_at: String,
    projects: Vec<ProjectRecord>,
    specs: Vec<CatalogSpec>,
    saved_views: Vec<SavedView>,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct CatalogSpec {
    id: String,
    project_id: String,
    title: String,
    source_path: String,
    active: bool,
    stage: Stage,
    status: Status,
    updated_at: String,
    last_visited_at: Option<String>,
}

pub(super) fn catalog(state: &WorkbenchState) -> Catalog {
    Catalog {
        revision: state.revision,
        read_at: sources::now(),
        projects: state.projects.clone(),
        specs: state
            .specs
            .iter()
            .map(|spec| CatalogSpec {
                id: spec.id.clone(),
                project_id: spec.project_id.clone(),
                title: spec.title.clone(),
                source_path: spec.source_path.clone(),
                active: spec.active,
                stage: spec.stage,
                status: spec.status,
                updated_at: spec.updated_at.clone(),
                last_visited_at: spec.last_visited_at.clone(),
            })
            .collect(),
        saved_views: state.saved_views.clone(),
    }
}

pub(super) struct OverviewBase {
    stamp: (u64, u64),
    recent: Vec<SpecRecord>,
    activity: Vec<ChangeEvent>,
    active_spec_count: usize,
    project_count: usize,
}

pub(super) fn overview_base(state: &WorkbenchState, stamp: (u64, u64)) -> OverviewBase {
    let active_projects: std::collections::BTreeSet<_> = state
        .projects
        .iter()
        .filter(|project| project.active)
        .map(|project| &project.id)
        .collect();
    let mut active: Vec<_> = state
        .specs
        .iter()
        .filter(|spec| spec.active && active_projects.contains(&spec.project_id))
        .collect();
    let active_spec_count = active.len();
    active.sort_by_cached_key(|spec| {
        Reverse((
            OffsetDateTime::parse(&spec.updated_at, &Rfc3339).ok(),
            spec.updated_at.as_str(),
        ))
    });
    OverviewBase {
        stamp,
        recent: active.into_iter().take(5).cloned().collect(),
        activity: state
            .history
            .iter()
            .rev()
            .filter(|event| event.task_id.is_none())
            .take(8)
            .cloned()
            .collect(),
        active_spec_count,
        project_count: state.projects.len(),
    }
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(super) struct BoardRequest {
    group_by: GroupBy,
    column: Option<String>,
}

const STAGE_COLUMNS: [&str; 9] = [
    "unmarked",
    "research",
    "analysis",
    "spec",
    "prototype",
    "handoff",
    "implementation",
    "verification",
    "delivery",
];
const STATUS_COLUMNS: [&str; 6] = ["unmarked", "todo", "doing", "blocked", "done", "canceled"];

struct QueryPage<'a> {
    board: Option<&'a BoardRequest>,
    total: usize,
    list_seen: usize,
    items: Vec<Value>,
    column_totals: BTreeMap<&'static str, usize>,
}

impl<'a> QueryPage<'a> {
    fn new(board: Option<&'a BoardRequest>) -> Result<Self> {
        let columns: &[&str] = match board.map(|board| board.group_by) {
            Some(GroupBy::Stage) => &STAGE_COLUMNS,
            Some(GroupBy::Status) => &STATUS_COLUMNS,
            None => &[],
        };
        if board
            .and_then(|value| value.column.as_deref())
            .is_some_and(|column| !columns.contains(&column))
        {
            return Err(sources::invalid("看板分页列无效"));
        }
        Ok(Self {
            board,
            total: 0,
            list_seen: 0,
            items: Vec::new(),
            column_totals: columns.iter().map(|column| (*column, 0)).collect(),
        })
    }

    fn includes(&mut self, item: &WorkItem, offset: usize, limit: usize) -> bool {
        let index = if let Some(board) = self.board {
            let column = match board.group_by {
                GroupBy::Stage => match item.stage {
                    Stage::Unmarked => "unmarked",
                    Stage::Research => "research",
                    Stage::Analysis => "analysis",
                    Stage::Spec => "spec",
                    Stage::Prototype => "prototype",
                    Stage::Handoff => "handoff",
                    Stage::Implementation => "implementation",
                    Stage::Verification => "verification",
                    Stage::Delivery => "delivery",
                },
                GroupBy::Status => match item.status {
                    Status::Unmarked => "unmarked",
                    Status::Todo => "todo",
                    Status::Doing => "doing",
                    Status::Blocked => "blocked",
                    Status::Done => "done",
                    Status::Canceled => "canceled",
                },
            };
            let count = self.column_totals.entry(column).or_default();
            let index = *count;
            *count += 1;
            if board
                .column
                .as_deref()
                .is_some_and(|chosen| chosen != column)
            {
                return false;
            }
            index
        } else {
            let index = self.list_seen;
            self.list_seen += 1;
            index
        };
        index >= offset && index - offset < limit
    }
}

fn compact_progress(progress: &ProgressSummary) -> Value {
    json!({
        "goalVersion": progress.goal_version,
        "primaryStage": progress.primary_stage,
        "status": progress.status,
        "validity": progress.validity,
        "attention": progress.attention,
        "summary": progress.summary,
        "blocker": progress.blocker,
        "nextAction": progress.next_action,
        "lastVerifiedAt": progress.last_verified_at,
        "evidenceDigest": progress.evidence_digest,
        "hasOverride": progress.has_override,
        "materialReview": progress.material_review,
        "activeStages": progress.active_stages,
        "lastDelivery": progress.last_delivery.as_ref().map(|decision| json!({ "recordedAt": decision.recorded_at })),
        "stages": progress.stages.iter().map(|stage| json!({ "stage": stage.stage, "progress": stage.progress })).collect::<Vec<_>>(),
    })
}

fn compact_item(view: &Snapshot, item: WorkItem) -> Result<Value> {
    let progress = if item.entity == WorkEntity::Spec {
        view.progress.get(&item.spec_id).map(compact_progress)
    } else {
        None
    };
    let mut value = serde_json::to_value(item).map_err(|_| sources::invalid("规格摘要编码失败"))?;
    value["progress"] = json!(progress);
    Ok(value)
}

#[derive(Default, Serialize)]
struct Bucket {
    total: usize,
    items: Vec<Value>,
}

struct Overview {
    groups: BTreeMap<&'static str, Bucket>,
    reminders: BTreeMap<&'static str, Bucket>,
}

impl Default for Overview {
    fn default() -> Self {
        Self {
            groups: ["attention", "blocked", "doing", "source"]
                .into_iter()
                .map(|kind| (kind, Bucket::default()))
                .collect(),
            reminders: query::REMINDER_KINDS
                .into_iter()
                .map(|kind| (kind, Bucket::default()))
                .collect(),
        }
    }
}

fn overview(view: &Snapshot) -> Result<Overview> {
    let mut overview = Overview::default();
    let content: BTreeMap<_, _> = view
        .specs
        .iter()
        .map(|spec| (&spec.record.id, &spec.content))
        .collect();
    let work_items = query::filter_items(
        view,
        &WorkQuery {
            include_closed: true,
            progress_basis: ProgressBasis::Effective,
            ..WorkQuery::default()
        },
    )?;
    for item in work_items.items {
        let content = content.get(&item.spec_id).expect("projected Spec");
        for (kind, bucket) in &mut overview.reminders {
            let count = item.reminder_counts.get(*kind).copied().unwrap_or_default();
            if count == 0 {
                continue;
            }
            bucket.total += count;
            if bucket.items.len() == 20 {
                continue;
            }
            let questions: Vec<_> = content
                .questions
                .iter()
                .filter(|question| query::matches_reminder(question, kind))
                .take(3)
                .map(|question| {
                    json!({
                        "id":question.question.id, "title":question.question.title,
                        "blocking":question.question.blocking
                    })
                })
                .collect();
            bucket.items.push(json!({
                "specId":item.spec_id, "title":item.title,
                "sourcePath":item.source_path, "projectName":item.project_name, "count":count,
                "questions":questions
            }));
        }
        let in_attention = view
            .progress
            .get(&item.spec_id)
            .is_some_and(|progress| !progress.attention.is_empty());
        let membership = [
            ("attention", in_attention),
            ("blocked", item.status == Status::Blocked),
            ("doing", item.status == Status::Doing),
            ("source", item.has_source_problem),
        ];
        let item = compact_item(view, item)?;
        for (group, includes) in membership {
            if includes {
                let bucket = overview.groups.get_mut(group).expect("overview group");
                bucket.total += 1;
                if bucket.items.len() < 20 {
                    bucket.items.push(item.clone());
                }
            }
        }
    }
    Ok(overview)
}
