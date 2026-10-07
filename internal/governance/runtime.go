package governance

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"github.com/iloveZzz/yss-cli/internal/worklayout"
	_ "modernc.org/sqlite"
)

const runtimeDDL = `
CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL);
CREATE TABLE runs(id TEXT PRIMARY KEY, kind TEXT NOT NULL, root TEXT NOT NULL, started_at TEXT NOT NULL, ended_at TEXT, status TEXT NOT NULL, exit_code INTEGER, input_digest TEXT, run_dir TEXT NOT NULL, report_dir TEXT, owner_pid INTEGER, hostname TEXT, logs_expired INTEGER NOT NULL DEFAULT 0);
CREATE TABLE commands(id INTEGER PRIMARY KEY, run_id TEXT NOT NULL REFERENCES runs(id), recorded_at TEXT NOT NULL, result_json TEXT NOT NULL);
CREATE TABLE events(id INTEGER PRIMARY KEY, run_id TEXT NOT NULL REFERENCES runs(id), recorded_at TEXT NOT NULL, type TEXT NOT NULL, value_json TEXT NOT NULL);
CREATE TABLE objects(digest TEXT PRIMARY KEY, bytes INTEGER NOT NULL, compressed_bytes INTEGER NOT NULL, relative_path TEXT NOT NULL, deleting INTEGER NOT NULL DEFAULT 0);
CREATE TABLE run_objects(run_id TEXT NOT NULL REFERENCES runs(id), digest TEXT NOT NULL REFERENCES objects(digest), PRIMARY KEY(run_id,digest));
CREATE TABLE pins(run_id TEXT NOT NULL REFERENCES runs(id), reason TEXT NOT NULL, created_at TEXT NOT NULL, PRIMARY KEY(run_id,reason));
CREATE TABLE run_files(run_id TEXT NOT NULL REFERENCES runs(id), path TEXT NOT NULL, sha256 TEXT NOT NULL, bytes INTEGER NOT NULL, PRIMARY KEY(run_id,path));
CREATE TABLE checkpoint_index(path TEXT PRIMARY KEY, source_digest TEXT NOT NULL, indexed_at TEXT NOT NULL, value_json TEXT NOT NULL);
PRAGMA user_version=1;`

var registeredPathPattern = regexp.MustCompile(`["']?(?:local_worktree|project_root|repository_root|local_path|root_path)["']?\s*:\s*("(?:\\.|[^"\\])*"|'(?:''|[^'])*'|[^\r\n"'#,}]+)|\|\s*(?:local_worktree|project_root|repository_root|local_path|root_path)\s*\|\s*([^|\r\n]+)`)
var gitlinkPattern = regexp.MustCompile(`(?m)^\s*path\s*=\s*(.+)$`)

func protectedRoots(root string) ([]string, error) {
	roots := []string{root}
	read := func(p string) error {
		b, err := os.ReadFile(p)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		for _, match := range registeredPathPattern.FindAllStringSubmatch(string(b), -1) {
			s := strings.TrimSpace(match[1])
			if s == "" {
				s = strings.TrimSpace(match[2])
			}
			if strings.HasPrefix(s, "\"") {
				if err = json.Unmarshal([]byte(s), &s); err != nil {
					return domain.Fail("PATH", "已登记仓库路径无法解析")
				}
			} else if strings.HasPrefix(s, "'") {
				s = strings.ReplaceAll(strings.TrimSuffix(strings.TrimPrefix(s, "'"), "'"), "''", "'")
			} else {
				s = strings.Trim(s, "`")
			}
			if s == "" {
				continue
			}
			if !filepath.IsAbs(s) {
				s = filepath.Join(root, s)
			}
			real, err := filepath.EvalSymlinks(s)
			if err == nil {
				roots = append(roots, real)
			} else if !os.IsNotExist(err) {
				return err
			}
		}
		return nil
	}
	scanRoots, err := worklayout.ReadScanRoots(root)
	if err != nil {
		return nil, err
	}
	for _, ref := range append(scanRoots, "docs", ".template-spec/implementation", ".template-spec/projects", ".template-spec/project") {
		p, err := safefs.Path(root, ref)
		if err != nil {
			return nil, err
		}
		err = filepath.WalkDir(p, func(p string, d os.DirEntry, e error) error {
			if os.IsNotExist(e) {
				return nil
			}
			if e != nil {
				return e
			}
			if d.Type()&os.ModeSymlink != 0 {
				return domain.Fail("PATH", "登记扫描拒绝符号链接: "+p)
			}
			if d.IsDir() {
				if d.Name() == ".git" || d.Name() == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			ext := filepath.Ext(p)
			if ext == ".md" || ext == ".json" || ext == ".yaml" || ext == ".yml" {
				return read(p)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	p, err := safefs.Path(root, ".gitmodules")
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, match := range gitlinkPattern.FindAllStringSubmatch(string(b), -1) {
		ref := strings.TrimSpace(match[1])
		p, err := safefs.Path(root, ref)
		if err != nil {
			return nil, err
		}
		if real, err := filepath.EvalSymlinks(p); err == nil {
			roots = append(roots, real)
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	return roots, nil
}

func runtimeLocation(root, home string) (string, error) {
	if home == "" {
		home = os.Getenv("YSS_RUNTIME_HOME")
	}
	if home == "" {
		base, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		home = filepath.Join(base, ".yss-harness", "runtime")
	}
	home, err := externalPath(root, home)
	if err != nil {
		return "", err
	}
	protected, err := protectedRoots(root)
	if err != nil {
		return "", err
	}
	for _, p := range protected {
		if inside(p, home) {
			return "", domain.Fail("PATH", "运行存储不得进入已登记仓库: "+p)
		}
	}
	dir := filepath.Join(home, safefs.Digest([]byte(root)))
	if _, err = safefs.Path(dir, "runtime.sqlite"); err != nil {
		return "", err
	}
	return dir, nil
}

func openRuntime(ctx context.Context, dir string, readOnly bool) (*sql.DB, error) {
	return openRuntimeMode(ctx, dir, readOnly, false)
}

func openRuntimeMode(ctx context.Context, dir string, readOnly, existingOnly bool) (*sql.DB, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file := filepath.Join(dir, "runtime.sqlite")
	fileExists := false
	for _, suffix := range []string{"", "-journal", "-wal", "-shm"} {
		st, err := os.Lstat(file + suffix)
		if suffix == "" && err == nil {
			fileExists = true
		}
		if err == nil && (!st.Mode().IsRegular() || st.Mode()&os.ModeSymlink != 0) {
			return nil, domain.Fail("RUNTIME", "数据库或sidecar必须是普通文件")
		}
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	mode := "rwc"
	if existingOnly {
		mode = "rw"
		if _, err := os.Stat(file); os.IsNotExist(err) {
			return nil, domain.Fail("RUNTIME", "运行数据库不存在，拒绝创建")
		} else if err != nil {
			return nil, err
		}
	}
	if readOnly {
		mode = "ro"
		if _, err := os.Stat(file); os.IsNotExist(err) {
			return nil, nil
		} else if err != nil {
			return nil, err
		}
	} else {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
	}
	if fileExists {
		// SQLite may create a -shm file while opening a WAL database, including
		// before an eventual mutation refusal. Native records use DELETE journals;
		// refuse all existing WAL opens instead of silently reconfiguring a store
		// or using an inconsistent immutable view that ignores pending pages.
		f, err := os.Open(file)
		if err != nil {
			return nil, err
		}
		header := make([]byte, 20)
		n, readErr := io.ReadFull(f, header)
		closeErr := f.Close()
		if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if n == len(header) && string(header[:16]) == "SQLite format 3\x00" && (header[18] == 2 || header[19] == 2) {
			return nil, domain.Fail("UNPORTED", "WAL 运行存储读写尚未迁移；拒绝重配数据库或更改 SQLite sidecar")
		}
	}
	uriPath := filepath.ToSlash(file)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath}
	q := u.Query()
	q.Set("mode", mode)
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	fail := func(err error) (*sql.DB, error) { _ = db.Close(); return nil, err }
	// modernc's SQLite busy handler can wait inside a native call without
	// honoring context cancellation. Bound that wait and report RUNTIME_BUSY
	// explicitly while ctx is still valid; cancellation never waits five seconds
	// for an unrelated connection to release its lock.
	if _, err = db.ExecContext(ctx, "PRAGMA busy_timeout=25; PRAGMA foreign_keys=ON;"); err != nil {
		return fail(err)
	}
	var version int
	if err = db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fail(err)
	}
	rows, err := db.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")
	if err != nil {
		return fail(err)
	}
	tables := map[string]bool{}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			_ = rows.Close()
			return fail(err)
		}
		tables[name] = true
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return fail(err)
	}
	if version == 0 && len(tables) == 0 && !readOnly && !existingOnly {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fail(err)
		}
		if _, err = tx.ExecContext(ctx, runtimeDDL); err != nil {
			_ = tx.Rollback()
			return fail(err)
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO schema_migrations VALUES(1,?)", time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			_ = tx.Rollback()
			return fail(err)
		}
		if err = tx.Commit(); err != nil {
			return fail(err)
		}
		version = 1
		for _, name := range []string{"schema_migrations", "runs", "commands", "events", "objects", "run_objects", "pins", "run_files", "checkpoint_index"} {
			tables[name] = true
		}
	}
	if version != 1 {
		return fail(domain.Fail("RUNTIME_SCHEMA", "未知运行数据库 schema，拒绝读写"))
	}
	for _, name := range []string{"schema_migrations", "runs", "commands", "events", "objects", "run_objects", "pins", "run_files", "checkpoint_index"} {
		if !tables[name] {
			return fail(domain.Fail("RUNTIME_SCHEMA", "运行数据库 schema 不完整: "+name))
		}
	}
	var migration int
	if err = db.QueryRowContext(ctx, "SELECT version FROM schema_migrations WHERE version=1").Scan(&migration); err != nil {
		if err == sql.ErrNoRows {
			return fail(domain.Fail("RUNTIME_SCHEMA", "运行数据库迁移记录缺失"))
		}
		return fail(err)
	}
	var check string
	if err = db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&check); err != nil {
		return fail(err)
	}
	if check != "ok" {
		return fail(domain.Fail("RUNTIME_INTEGRITY", "运行数据库完整性检查失败"))
	}
	if !readOnly {
		pragmas := "PRAGMA synchronous=FULL;"
		if !fileExists {
			pragmas = "PRAGMA journal_mode=DELETE; " + pragmas
		}
		if _, err = db.ExecContext(ctx, pragmas); err != nil {
			return fail(err)
		}
	}
	return db, nil
}

func runtimeOwner(dir, id, token, root string) error {
	if !regexp.MustCompile(`^[A-Za-z0-9-]{1,120}$`).MatchString(id) || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(token) {
		return domain.Fail("RUNTIME_OWNER", "需要有效运行 ID 与创建运行时返回的 --token")
	}
	p, err := safefs.Path(dir, "runs/"+id+"/.go-owner.json")
	if err != nil {
		return err
	}
	m, err := schemaOwner(p)
	if err != nil {
		return domain.Fail("RUNTIME_OWNER", "旧运行或未绑定所有权，拒绝更改")
	}
	if text(m["root"]) != root {
		return domain.Fail("RUNTIME_OWNER", "运行所有权记录与项目身份不匹配")
	}
	actual := safefs.Digest([]byte(token))
	if subtle.ConstantTimeCompare([]byte(text(m["token_digest"])), []byte(actual)) != 1 {
		return domain.Fail("RUNTIME_OWNER", "运行所有权 token 不匹配")
	}
	return nil
}
func schemaOwner(p string) (map[string]any, error) {
	st, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, domain.Fail("RUNTIME_OWNER", "所有权记录必须是普通文件")
	}
	v, err := schema.LoadFile(p)
	if err != nil {
		return nil, err
	}
	m, ok := object(v)
	if !ok {
		return nil, domain.Fail("RUNTIME_OWNER", "所有权记录格式非法")
	}
	version, ok := integer(m["version"])
	if !ok || version != 1 {
		return nil, domain.Fail("RUNTIME_OWNER", "所有权协议版本未知")
	}
	return m, nil
}

func runtimeRun(ctx context.Context, action, root string, args map[string]string) (out any, err error) {
	defer func() {
		if err != nil {
			err = runtimeFailure(ctx, err)
			out = nil
		}
	}()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if action != "inspect" && action != "begin" && action != "event" && action != "complete" && !runtimeQueryAction(action) && action != "pin" && action != "unpin" {
		return nil, domain.Fail("UNPORTED", "运行存储动作尚未迁移: "+action)
	}
	if action == "begin" && (args["input"] != "" || args["report-dir"] != "") {
		return nil, domain.Fail("UNPORTED", "输入对象与正式报告保护尚未迁移")
	}
	dir, err := runtimeLocation(root, args["home"])
	if err != nil {
		return nil, err
	}
	if runtimeQueryAction(action) || action == "pin" || action == "unpin" {
		id, err := runtimeExplicitID(args)
		if err != nil {
			return nil, err
		}
		if runtimeQueryAction(action) {
			return runtimeQuery(ctx, action, root, dir, id)
		}
		return runtimePin(ctx, action, root, dir, id, args["token"], args["reason"])
	}
	if action == "event" || action == "complete" {
		id := first(args["id"], args["arg0"])
		if err = runtimeOwner(dir, id, args["token"], root); err != nil {
			return nil, err
		}
	}
	readOnly := action == "inspect"
	db, err := openRuntimeMode(ctx, dir, readOnly, action != "begin" && !readOnly)
	if err != nil {
		return nil, err
	}
	if db != nil {
		defer db.Close()
	}
	if readOnly {
		runs := []map[string]any{}
		if db != nil {
			rows, err := db.QueryContext(ctx, "SELECT r.id,r.kind,r.root,r.started_at,r.ended_at,r.status,r.exit_code,r.run_dir,r.logs_expired,(SELECT count(*) FROM events e WHERE e.run_id=r.id) FROM runs r ORDER BY r.started_at,r.id")
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			for rows.Next() {
				var id, kind, recordRoot, started, status, runDir string
				var ended sql.NullString
				var exit sql.NullInt64
				var expired, events int64
				if err = rows.Scan(&id, &kind, &recordRoot, &started, &ended, &status, &exit, &runDir, &expired, &events); err != nil {
					return nil, err
				}
				var endValue, exitValue any
				if ended.Valid {
					endValue = ended.String
				}
				if exit.Valid {
					exitValue = exit.Int64
				}
				runs = append(runs, map[string]any{"id": id, "kind": kind, "root": recordRoot, "started_at": started, "ended_at": endValue, "status": status, "exit_code": exitValue, "run_dir": runDir, "logs_expired": expired != 0, "events": events})
			}
			if err = rows.Err(); err != nil {
				return nil, err
			}
		}
		return map[string]any{"schema_version": 1, "read_only": true, "directory": dir, "database_exists": db != nil, "runs": runs, "scope": "runtime-records-only", "reference_scan": "UNPORTED", "execution_authorization": "not-evaluated"}, nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if action == "begin" {
		kind := strings.TrimSpace(args["kind"])
		if kind == "" {
			kind = "command"
		}
		if args["input"] != "" || args["report-dir"] != "" {
			return nil, domain.Fail("UNPORTED", "输入对象与正式报告保护尚未迁移")
		}
		random := make([]byte, 32)
		if _, err = rand.Read(random); err != nil {
			return nil, err
		}
		token := hex.EncodeToString(random)
		id := time.Now().UTC().Format("20060102T150405.000000000") + "-" + token[:24]
		id = strings.ReplaceAll(id, ".", "")
		runDir, err := safefs.Path(dir, "runs/"+id)
		if err != nil {
			return nil, err
		}
		if err = os.MkdirAll(filepath.Dir(runDir), 0700); err != nil {
			return nil, err
		}
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		if err = os.Mkdir(runDir, 0700); err != nil {
			return nil, err
		}
		created := false
		defer func() {
			if !created {
				_ = os.RemoveAll(runDir)
			}
		}()
		b, _ := json.Marshal(map[string]any{"version": 1, "token_digest": safefs.Digest([]byte(token)), "root": root})
		if err = os.WriteFile(filepath.Join(runDir, ".go-owner.json"), b, 0600); err != nil {
			return nil, err
		}
		host, _ := os.Hostname()
		if _, err = tx.ExecContext(ctx, "INSERT INTO runs(id,kind,root,started_at,status,run_dir,owner_pid,hostname) VALUES(?,?,?,?,?,?,?,?)", id, kind, root, time.Now().UTC().Format(time.RFC3339Nano), "running", runDir, os.Getpid(), host); err != nil {
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		created = true
		return map[string]any{"id": id, "token": token, "run_dir": runDir, "directory": dir, "status": "running", "execution_authorization": "not-evaluated"}, nil
	}
	id := args["id"]
	if id == "" {
		id = args["arg0"]
	}
	if err = runtimeOwner(dir, id, args["token"], root); err != nil {
		return nil, err
	}
	var recordedRoot, runDir, status string
	var ended sql.NullString
	var expired int
	if err = tx.QueryRowContext(ctx, "SELECT root,run_dir,status,ended_at,logs_expired FROM runs WHERE id=?", id).Scan(&recordedRoot, &runDir, &status, &ended, &expired); err != nil {
		return nil, domain.Fail("RUNTIME", "未知运行 ID")
	}
	if recordedRoot != root || runDir != filepath.Join(dir, "runs", id) {
		return nil, domain.Fail("RUNTIME_OWNER", "运行身份或目录不匹配")
	}
	if ended.Valid || expired != 0 {
		return nil, domain.Fail("RUNTIME_STATE", "运行已结束或进入清理状态")
	}
	if action == "event" {
		typeName := strings.TrimSpace(args["type"])
		if typeName == "" {
			return nil, domain.Fail("INPUT", "event 需要非空 --type")
		}
		value := args["value"]
		if value == "" {
			value = "null"
		}
		if !json.Valid([]byte(value)) {
			return nil, domain.Fail("INPUT", "event --value 必须是有效 JSON")
		}
		if _, err = schema.Parse([]byte(value)); err != nil {
			return nil, domain.Wrap("INPUT", err)
		}
		inserted, err := tx.ExecContext(ctx, "INSERT INTO events(run_id,recorded_at,type,value_json) SELECT ?,?,?,? WHERE EXISTS(SELECT 1 FROM runs WHERE id=? AND ended_at IS NULL AND logs_expired=0)", id, time.Now().UTC().Format(time.RFC3339Nano), typeName, value, id)
		if err != nil {
			return nil, err
		}
		n, err := inserted.RowsAffected()
		if err != nil {
			return nil, err
		}
		if n != 1 {
			return nil, domain.Fail("RUNTIME_STATE", "运行已被其他调用完成")
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return map[string]any{"id": id, "event_type": typeName, "execution_authorization": "not-evaluated"}, nil
	}
	terminal := args["status"]
	allowed := map[string]bool{"passed": true, "success": true, "completed": true, "ok": true, "failed": true, "failure": true, "cancelled": true, "canceled": true, "timed-out": true, "timeout": true, "error": true}
	if !allowed[terminal] {
		return nil, domain.Fail("INPUT", "complete 需要已知终态 --status")
	}
	exit, err := strconv.Atoi(args["exit-code"])
	if err != nil {
		return nil, domain.Fail("INPUT", "complete 需要实际整数 --exit-code")
	}
	if (terminal == "passed" || terminal == "success" || terminal == "completed" || terminal == "ok") && exit != 0 {
		return nil, domain.Fail("INPUT", "成功终态与非零退出码冲突")
	}
	result, err := tx.ExecContext(ctx, "UPDATE runs SET status=?,exit_code=?,ended_at=?,owner_pid=NULL WHERE id=? AND ended_at IS NULL AND logs_expired=0", terminal, exit, time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return nil, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n != 1 {
		return nil, domain.Fail("RUNTIME_STATE", "运行已被其他调用完成")
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "status": terminal, "exit_code": exit, "run_dir": runDir, "scope": "runtime-record-only", "lifecycle_approval": false, "execution_authorization": "not-evaluated"}, nil
}
