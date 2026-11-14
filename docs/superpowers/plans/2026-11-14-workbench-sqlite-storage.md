# 工作台 SQLite 存储升级实施计划

> **For agentic workers:** 使用 `superpowers:executing-plans` 按依赖顺序实施。本支线不使用子代理；用户已于 2026-11-14 明确批准执行，实施与核验记录见文末。

**Goal:** 将个人工作台运行状态迁至 SQLite，实现按记录事务写入、旧数据完整迁移和旧版防误写，保持现有业务行为。

**Architecture:** 保留 `WorkbenchState`、业务规则及内存查询，SQLite 负责逐记录保存和事务。启动/写入路径持有既有目录锁后执行迁移或恢复；纯查询按存储标记选择旧 JSON 或只读 SQLite。迁移使用备份、临时库和 preparing/active 标记处理跨文件切换。

**Tech Stack:** Rust/Cargo 1.99.1、`rusqlite` + `bundled`、SQLite、现有 `serde_json`/codec/fs；Vue UI 不改动。

**Spec:** [已确认的存储设计](../specs/2026-11-14-workbench-sqlite-storage-design.md)，实施前必须与本计划一起阅读。设计基线 HEAD 为 `f6e26815246e40b868e18e838b504a0a4e425b1f`，执行时核对工作树变化。

## Global Constraints

- 2026-11-14 已获代码实施授权；不迁移真实用户数据，不提交、合并、推送或发行。
- 执行时保留现有未提交工作，按当时的隔离要求选择工作区；不得重置、搬走或覆盖另一轮 UI 改动。只修改本计划列出的存储相关文件。
- 主库 `workbench.sqlite3`；沿用现有默认目录和 `--data-dir`，禁止项目/Git 工作树内的数据目录。
- `journal_mode=DELETE`、`synchronous=EXTRA`、`foreign_keys=ON`、`busy_timeout=1000ms`；只读连接不执行持久化设置，不启用 WAL。
- `PRAGMA user_version=1`、`application_id=0x59535357`；业务模型 `schemaVersion=3`；业务 revision 不超过 `9_007_199_254_740_991`。
- 存储标记 `schemaVersion=0`、`markerVersion=1`；纯查询不迁移，未知格式和状态冲突明确报错，不能回退为新空状态。
- 保留全部记录和顺序，包括隐藏任务、历史的任意 JSON 值、资源来源、问题答复、时区、确认和保存视图。
- 保留逻辑状态 16 MiB、codec 深度 128、节点 1,000,000、数字文本 4096 字节及既有响应限制；不以此限制 SQLite 物理文件大小。
- 不新增全文检索、服务端分页、双写、后台任务、公开迁移命令或通用存储抽象。
- 临时验证材料放系统临时目录，必要的临时测试接线在验证后移除；不维护或分发测试套件、包装脚本、故障注入开关。
- 当前发行目标按已有说明为 macOS arm64、Windows x86_64；分别报告构建与原生运行范围，不因某个平台通过就外推其他平台。

## Review Focus

1. 旧数据已接近容量上限，补齐 v3 字段后超限：应在切换前拒绝，不删记录、不留下假成功。归属任务 3。
2. 标记已切换而数据库尚未发布时退出：只读命令应报告未完成，启动路径可续接，旧版不能再写 JSON。归属任务 3、4。
3. 提交可能成功但响应/后续校验失败：不得自动重放答复或恢复旧快照覆盖新数据。归属任务 2、4。
4. 旧工作台持锁、会话残留或转发连接失败：不得绕过迁移顺序或把结果不确定的请求再本地提交。归属任务 4。
5. 数据库路径/日志被替换，或只读打开遇到热日志：应拒绝不安全路径；只读操作不得执行恢复写入。归属任务 1、4。

## 文件与责任

| 文件 | 变更 |
| --- | --- |
| `Cargo.toml`、`Cargo.lock` | 添加经 Rust 1.99.1 验证的 rusqlite bundled 依赖 |
| `src/workbench/sqlite.rs`（新增） | 表结构、payload 编解码、数据库身份、事务、增量 SQL 与有界读取 |
| `src/workbench/storage_migration.rs`（新增） | 标记识别、备份、临时库发布、迁移续接；与原 JSON 解码分开 |
| `src/workbench/store.rs` | 保留公共接口，接入新数据库与迁移；复用校验 |
| `src/workbench/migration.rs` | 保留 v1/v2/v3 解码；仅做必要的复用调整 |
| `src/workbench/mod.rs` | 注册两个新模块，默认 crate 内可见，不扩大公共 API |
| `src/cli.rs`、`session.rs`、`server.rs` | 存储状态分流、会话预检与启动提示 |
| `src/fs/mod.rs` 及必要 platform 文件 | 仅补充受控且不读取整文件的身份检查 |
| `docs/workbench.md`、`docs/build-install.md` | 升级、回退边界、数据位置及 bundled 构建说明 |

`model.rs`、`service.rs`、`registration.rs`、`content.rs`、`query.rs` 为语义基准。不得因存储改造顺带重构这些业务规则；必要的可见性或接线变化须说明原因。

## 验证约定

以下是原定实施步骤，实际执行方式及结果见文末。先检查是否已有与当前输入一致的 `ui/dist` 和 `assets/bundle.tar.gz`；缺失时在实施工作区按 `docs/build-install.md` 使用明确的模板来源生成。只为取得构建输入运行 UI 构建，不修改 UI 源文件。

临时 Rust 断言可用模块私有测试接线引入系统临时目录中的文件；模块名固定为下文的 `sqlite_storage_probe`、`storage_migration_probe`、`store_integration_probe`。这些文件、`#[cfg(test)]` 接线及故障注入在最后移除，不提交。临时夹具覆盖旧 JSON v1/v2/v3；v3 至少含所有记录类型、非 ASCII 文本、任意精度数字历史、无效但原先允许的历史关联、条件/日期延期及隐藏任务。

测试命令过滤到对应模块，预期至少执行一条命名场景且全部通过；“0 tests”不算通过。命令输出和候选源码摘要作为当次执行证据即可，不补建长期检查资产。

---

### Task 1: 数据库结构、记录往返与文件边界

**Files:** 新增 `src/workbench/sqlite.rs`；修改 `Cargo.toml`、`Cargo.lock`、`src/workbench/mod.rs`；按需最小修改 `src/fs`。临时断言位于系统临时目录，接线在 `sqlite.rs`。

**Interfaces:**

- 复用 `crate::error::Result`、`Root`、`FileIdentity`、`RelPath`、`WorkbenchState`。
- 在 `sqlite.rs` 定义 `pub(crate) struct DatabaseIdentity { pub id: String, pub source_sha256: Option<String>, pub initial_revision: u64 }`。
- 定义 `pub(crate) struct Database`，封装连接、文件身份、元信息与同一连接的 data_version 基线；不对外暴露连接。
- 提供以下 crate 内方法：

```rust
Database::create_staged(root: &Root, name: &RelPath,
    identity: &DatabaseIdentity, state: &WorkbenchState) -> Result<()>
Database::open_read_only(root: &Root, name: &RelPath) -> Result<Database>
Database::open_read_write(root: &Root, name: &RelPath) -> Result<Database>
Database::identity(&self) -> &DatabaseIdentity
Database::load_state(&mut self, root: &Root) -> Result<WorkbenchState>
```

- 若缺少无整库读取的身份能力，在 `Root` 增加 `pub(crate) fn regular_file_identity(&self, path: &RelPath) -> Result<FileIdentity>`；普通文件且路径/父目录身份稳定才返回，不得用有界整文件读取伪装元数据检查。

- [x] 选用兼容锁定工具链的 rusqlite 版本，只启用 bundled 所需能力，更新锁文件；验证 Rust 编译器和本地 C 编译环境，不升级其他依赖。
- [x] 实现设计 §4.1 的 9 张表：单行 meta 加 8 类记录。payload 为每条记录的完整 JSON；主键/关联列与 payload 一致；history 用 position 主键，不新增事件 ID 唯一性或外键要求。
- [x] 实现受控建库/打开、身份核对与配置读回；临时库独占预创建，POSIX 0600；只读显式 READ_ONLY，不设置 URI/CREATE/immutable；拒绝未知表结构、视图、触发器及不安全伴随路径。
- [x] 实现有界加载，单读事务重建所有数组；检查 position、显式列和 payload 一致性，保持任意精度 JSON。只有 create_staged 成功关闭数据库后才返回，保证可安全发布。
- [x] 编写临时 `roundtrip_v3`、`history_duplicate_ids_are_preserved`、`read_only_is_no_write`、`unsafe_database_paths_are_rejected` 场景。核心断言如下，变量来自上述构造与加载结果：

```rust
assert_eq!(loaded, original);
assert_eq!(loaded.revision, original.revision);
assert_eq!(loaded.history.len(), original.history.len());
assert_eq!(files_after_read, files_before_read);
assert_eq!(rejected.code, ErrorCode::UnsafePath);
```

- [x] 运行 `cargo test --locked --lib workbench::sqlite::sqlite_storage_probe -- --test-threads=1`，确认命名场景实际执行并通过。故意损坏 schema、payload 与关系列应被拒绝，不能载入空状态。

**完成结果：** 临时 SQLite 可完整往返现有模型，尚未切换工作台的实际存储入口。

### Task 2: 差异事务、revision 与结果不确定保护

**Files:** 修改 `src/workbench/sqlite.rs`、`store.rs` 的校验可见性；临时断言继续接线在 `sqlite.rs`。

**Interfaces:**

- 消费任务 1 的 Database 与身份接口。
- 将原 `store::validate` 作为 `pub(crate) fn validate_state(state: &WorkbenchState) -> Result<()>` 复用，保持规则不变；增加 `pub(crate) fn validate_state_limits(state: &WorkbenchState) -> Result<()>` 复用既有完整编码/解码容量检查。
- 增加 `Database::commit_delta(&mut self, root: &Root, expected_revision: u64, before: &WorkbenchState, next: &WorkbenchState) -> Result<()>`。调用方传入 `next.revision == expected_revision + 1`，其他不符合的 revision 组合拒绝。

- [x] 实现按主键/position 比较的差异计算，不引入通用同步框架；不变 payload 不 UPDATE，删除视图导致位置变化时只改必要位置，history 常规追加只 INSERT 新位置。
- [x] 在 `BEGIN IMMEDIATE` 内核对 database_id、数据库 revision 和同连接 data_version；应用差异与历史，最后条件更新 meta revision，确认仅影响一行；所有绑定使用参数化 SQL。
- [x] 显式处理回滚、BUSY、容量、磁盘错误；COMMIT 或后续核验结果不确定时返回 `RecoveryRequired`，不得自动重试业务事务。仅已明确回滚的事务允许下一次独立用户请求重试。
- [x] 编写临时 `delta_touches_only_changed_rows`、`stale_revision_writes_nothing`、`external_commit_is_detected`、`transaction_rolls_back_all_changes`、`uncertain_commit_is_not_replayed` 场景；以 SQL 观察/临时审计计数确认更新范围，不能只检查最终状态相等。
- [x] 对中途失败使用仅存在于临时验证接线的注入点：首条记录写入后、meta 更新前使事务退出；提交成功后使结果核验失败。前者断言原状态/revision 完整保留，后者断言磁盘最多提交一次且错误为 `RecoveryRequired`。
- [x] 重新运行任务 1 的过滤命令，执行新增场景；保留“更改 1 条记录不重写未变 payload”的实际观察结果，不据此声称整体查询变快。

**完成结果：** 数据库层具备增量写入、事务冲突及不确定结果处理；旧 JSON 入口仍未被替换。

### Task 3: 备份、存储标记与可续接迁移

**Files:** 新增 `src/workbench/storage_migration.rs`；修改 `mod.rs`、必要的 `migration.rs` 复用点及 `store.rs` 路径校验可见性；临时断言接线在新迁移模块。

**Interfaces:**

- 消费任务 1 的 Database/DatabaseIdentity，任务 2 的 validate_state/validate_state_limits，以及既有 `migration::decode_state`。
- 在新模块定义 crate 内 `MigrationPhase { Preparing, Active }`、`MigrationSource { Empty, LegacyJson { version: u32, backup_name: String, sha256: String } }`。
- 定义 crate 内 `StorageMarker`，字段至少包括 `schema_version: u32`、`marker_version: u32`、`storage: String`、`phase: MigrationPhase`、`database_name: String`、`database_id: String`、`staged_name: String`、`source: MigrationSource`、`initial_revision: u64`；序列化用 camelCase 且拒绝未知字段。
- 定义 `StorageState { Empty, Legacy, Preparing(StorageMarker), Active(StorageMarker) }`；无效组合通过 Result 错误表达，不归入 Empty。
- 提供 `pub(crate) fn probe(data_dir: &Path) -> Result<StorageState>`，全程只读；`pub(crate) fn ensure_sqlite(lock: &RootLock) -> Result<StorageMarker>`，只在持锁的写路径调用。
- 复用 `store::resolve_data_dir`，将原 `reject_git` 调整为 crate 内可见供 probe 使用；不复制一套较弱的目录保护。

- [x] 实现 probe：不存在目录/无状态可为 Empty；支持的 JSON 为 Legacy；合法标记按 phase 分流；任何正式库与标记冲突都报错。限制标记大小为 16 KiB，名称只能是程序允许的本目录文件名。
- [x] 实现逐字节备份及重复备份校验，复用 `state.v{version}-backup-{sha256}.json` 命名和不覆盖语义；在首次写入前完成 v3 状态/容量校验，并复用原 Store 对“数据目录位于登记项目内”的拒绝检查。
- [x] 按设计 §6.2 实现 ensure_sqlite：持锁检查 → 备份 → 临时库事务导入/关闭/验证 → preparing 标记 → 不覆盖发布正式库 → active 标记；迁移保持业务 revision，不产生历史事件。
- [x] 实现设计 §6.3 的续接表：只处理标记绑定且可证明为初始导入内容的库；Empty 来源与默认模型比较。正式库丢失/损坏、两库并存、未知 markerVersion 等明确停止，不自动从备份覆盖现库。
- [x] 编写临时 `migrate_v1_v2_v3`、`migrate_empty_directory`、`migration_is_idempotent`、`backup_collision_preserves_original`、`normalized_state_over_limit_is_rejected`、`resume_preparing_states`、`unknown_state_is_not_empty` 场景。
- [x] 在备份完成、临时库提交、preparing 发布、正式库发布、active 发布五个位置，用临时子进程故障注入模拟异常退出；逐一断言下一次 probe/read/ensure 的结果与设计状态表一致。验证进程不是 Agent 子代理，且只操作临时目录。
- [x] 运行 `cargo test --locked --lib workbench::sqlite::sqlite_storage_probe -- --test-threads=1`；另外校验原 JSON 备份 SHA-256、记录数量、顺序、整个模型相等和 revision 相等。

**完成结果：** 临时目录迁移可重复、可续接；错误不会静默丢失旧数据或选择错误数据源。

### Task 4: Store、只读 CLI 与运行中工作台接入

**Files:** 修改 `store.rs`、`src/cli.rs`、`session.rs`、`server.rs`；必要时只调整既有调用的可见性；临时断言接线在 `store.rs`，真实进程验证材料位于系统临时目录。

**Interfaces:**

- 保留 `read_state(data_dir: &Path) -> Result<WorkbenchState>`、`Store::open(data_dir: &Path) -> Result<Store>`、`Store::state(&self) -> &WorkbenchState`、`Store::data_dir(&self) -> &Path`、`Store::commit(&mut self, expected_revision: u64, next: WorkbenchState) -> Result<u64>`。
- 保留现有 `session::mutate_if_running`、`register_if_running`、`publish` 的外部调用形式；内部会话读不再为预检加载完整业务状态。
- 消费任务 3 的 probe/ensure_sqlite，任务 2 的 commit_delta 和校验函数。

- [x] 改造 Store::open：prepare_dir → 目录锁 → ensure_sqlite → 打开写连接 → 受控加载；保存 Database 和 uncertain 状态，废除运行期 JSON 整文件替换。
- [x] 改造 Store::commit：沿用调用方 revision 检查与校验，计算下一 revision 并调用 commit_delta；成功后才换内存状态，RecoveryRequired 后阻止后续写入。复查所有现有调用点，避免重复增加 revision。
- [x] 改造 read_state：按 probe 分流；Empty 不创建文件；Legacy 只解码；Preparing 报未完成；Active 在只读一致事务中加载并匹配 marker/database_id。热日志需写恢复时只提示启动新版，不能删除日志或改用写连接。
- [x] 调整 register/respond 的入口：legacy/empty/preparing 直接走受锁保护的打开/恢复；active 才按原有规则转发会话。保留令牌、127.0.0.1/Origin/Host 和异常响应保护，网络结果不确定不能再本地执行同一请求；迁移提示写 stderr，不污染 JSON 信封或输出会话凭据。
- [x] 用临时 `store_integration_probe` 覆盖 revision 冲突、无变化、容量上限、隐藏任务、资源来源和确认摘要；对比迁移前后的 query/lifecycle/content 结果，忽略读取时刻等明确动态字段，不能忽略业务字段或顺序。
- [x] 运行 `cargo test --locked --lib workbench::sqlite::sqlite_storage_probe -- --test-threads=1`；随后在临时非 Git 数据目录运行真实新 CLI 的 snapshot/query/lifecycle/request/register/respond 和本地 HTTP 保存，证明接线正确。
- [x] 使用真实旧版二进制：旧工作台持锁时新版无法迁移；旧进程退出后新版完成迁移；旧版的启动、查询、注册和答复被标记阻止。若无法取得已校验的旧二进制，明确记录此项未验证，不用新版模拟通过。
- [x] 让临时写进程在事务中异常退出产生需恢复的日志：只读命令不得写文件，启动新版后能恢复；覆盖过期会话、转发失败、两个窗口提交同 revision，以及主文件/伴随路径替换。

**完成结果：** 现有工作台和 CLI 已统一使用新存储，旧 JSON 仅作未迁移输入或明确备份；业务规则与只读边界保持。

### Task 5: 使用说明、清理临时接线与最终候选验证

**Files:** 修改 `docs/workbench.md`、`docs/build-install.md`；清理任务 1–4 产生的临时测试接线及故障注入，不新增其他产品代码。

**Interfaces:** 使用前四项的公开 CLI 与 Store 接口，不引入新命令或配置。

- [x] 更新工作台文档：主库与标记文件角色、首次启动/写入迁移、纯查询只读、失败提示、需要先退出旧工作台、升级前备份不能包含升级后修改。回退示例只使用已有 `--data-dir` 和新独立目录。
- [x] 更新构建说明：rusqlite bundled 所需构建环境、用户无需安装 SQLite、现有平台范围及仍保留的逻辑容量限制；不更改版本号、Release 记录或模板固定来源。
- [x] 移除所有临时 #[cfg(test)] 接线、故障开关与探针文件引用，检查本次 diff 只包含列出的实现及文档；保留他人的 UI 改动，不用全仓清理命令删除未跟踪内容。
- [x] 在清理后的最终候选执行 `cargo fmt --all -- --check`、`cargo clippy --locked --all-targets -- -D warnings` 和 `cargo build --locked --release --bin yss --target aarch64-apple-darwin`。若需格式化，只格式化本次 Rust 文件，避免改动其他工作。
- [x] 用最终 release 二进制重新完成一次临时旧数据迁移 → 修改答复/视图 → 重启 → 只读查询往返；核对备份摘要、revision 和全部记录，不能只引用带临时注入接线的先前结果。
- [ ] 在 Windows x86_64 构建环境按既有方式设置 `RUSTFLAGS=-C target-feature=+crt-static`，执行对应 `--target x86_64-pc-windows-msvc` 的 locked release 构建；检查未依赖用户另装 SQLite DLL。没有 Windows 环境时将其列为未完成，不把交叉编译或静态检查称为原生交互验证。
- [x] 汇总实际变更、候选源码、命令、临时场景、原生运行平台、未完成事项和包体变化；不提交、不推送、不发布，除非届时另有对应授权。

**完成结果：** 可审阅的存储升级实现及实际验证证据，明确保留的容量/查询限制与平台证据边界。

## 计划自审与交接

已对照设计逐项检查：表/字段与顺序由任务 1 承接，事务和不确定结果由任务 2 承接，迁移/回退保护由任务 3 承接，所有 CLI/会话及只读行为由任务 4 承接，使用说明与最终候选由任务 5 承接。Review Focus 的五类风险都有所属验证步骤；接口名称与调用顺序已统一。

设计与计划确认后，用户另行明确批准执行。本支线由同一执行者按依赖顺序实现和作者自审，未安排子代理或独立审查；实现授权不扩展为合并或发行授权。

## 2026-11-14 实施记录

- 隔离目录：`/Users/zhudaoming/.codex/worktrees/workbench-sqlite/yss-cli`，分支 `codex/workbench-sqlite`，起点为上述基线 HEAD。未修改另一工作树中的 UI 工作。
- 任务 1–4 已接入：`rusqlite 0.42.0` bundled、逐记录表、差异事务、旧 JSON 自动迁移、可续接标记、只读分流及旧版阻断。沿用现有业务规则与公开接口。
- 实际接线仅需改动 `Store` 与 `session.rs`；现有 `cli.rs`、`server.rs` 调用链已能承接迁移和返回错误，无需重复增加入口逻辑。外键延迟到事务提交时检查，因此差异按固定表顺序执行；事务中允许短暂父子顺序变化，提交后不允许悬空关联。
- 临时验证集中在 `sqlite::sqlite_storage_probe` 子模块，以便直接观察 SQL 更新数量及注入故障；未按计划拆成三个测试模块。21 项临时检查通过，覆盖所有记录类型往返、v1/v2/v3/空目录迁移、五个迁移边界强制退出、备份冲突、容量、事务回滚、占锁、外部提交、提交结果不确定及查询/lifecycle 语义。故障注入与临时模块引用已移除。
- 作者自审修复两处问题并验证失败到通过：COMMIT 遇忙且已回滚时返回可重试的 `Locked`；结构容量超限保留原接口的 `InvalidInput` / HTTP 413。
- 最终候选执行通过：`cargo fmt --all -- --check`、`cargo clippy --locked --all-targets -- -D warnings`、`cargo build --locked --release --bin yss --target aarch64-apple-darwin`。构建输入由 `pnpm --dir ui build` 及现有 `yss-bundle` 生成，未修改 UI 源码。
- 最终 release 二进制通过真实 CLI/HTTP 往返：旧 3.4.1 进程占锁、旧 JSON 只读、启动迁移与逐字节备份、旧版启动/读写/转发阻断、答复待回写、视图保存和 HTTP 409、重启持久化、失效会话回退、真实热日志只读拒绝与启动恢复。追加验证损坏 payload、错误关联/顺序/库身份、硬链接、非私有权限、日志符号链接和运行中主库替换，均拒绝且未覆盖数据。全部数据位于独立临时目录。
- 构建来源：CLI 基线仍为 `f6e26815246e40b868e18e838b504a0a4e425b1f`，`source.state=working-tree`，源码摘要 `66d27a1cb2d34531ad7358a92552423e7f5c4102410e6c5ec4aa9c23082846b0`；bundle 摘要 `95c34265f53291189e84fe43f3ed123aedbb3a5efc0406cf7bbbb768e99803d1`，来自明确指定的模板工作树，不能当作固定发行来源。
- macOS arm64 二进制为 11,434,272 字节，SHA-256 `1fd25acc8ecdf0fc590a09387b40ae9f922460c60af8b87d6ec63ac4366d54b9`；已安装旧版为 9,454,576 字节，相差 1,979,696 字节。模板 bundle 来源有差异，这不是受控测量的 SQLite 单项增量。`otool -L` 未发现外部 SQLite 动态库依赖。
- 未验证：Windows 原生构建与运行（本机无 Windows 环境及相应 C/链接工具）、真实断电、恶意进程对文件路径的所有竞态组合。未迁移用户实际数据，未安装替换已有 CLI，未提交、推送、合并或发布。
