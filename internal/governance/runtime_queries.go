package governance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
)

var runtimeIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,120}$`)

func runtimeFailure(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var sqliteError interface{ Code() int }
	if errors.As(err, &sqliteError) {
		code := sqliteError.Code() & 0xff
		if code == 5 || code == 6 { // SQLITE_BUSY / SQLITE_LOCKED and extended forms.
			return domain.Wrap("RUNTIME_BUSY", err)
		}
	}
	return err
}

func runtimeQueryAction(action string) bool {
	return action == "run" || action == "events" || action == "commands" || action == "pins"
}

func runtimeExplicitID(args map[string]string) (string, error) {
	id, positional := args["id"], args["arg0"]
	if id != "" && positional != "" && id != positional {
		return "", domain.Fail("INPUT", "--id 与位置运行 ID 不一致")
	}
	id = first(id, positional)
	if !runtimeIDPattern.MatchString(id) {
		return "", domain.Fail("INPUT", "需要显式合法运行 ID；不接受路径或 SQL 表达式")
	}
	return id, nil
}

type runtimeQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// runtimeRecord reads identity from the selected database, never from a token or
// a caller-provided report path. Old records remain queryable without acquiring
// mutation authority over them.
func runtimeRecord(ctx context.Context, q runtimeQuerier, root, dir, id string) (map[string]any, error) {
	var kind, recordedRoot, started, status, runDir string
	var ended, input, report, host sql.NullString
	var exit, pid sql.NullInt64
	var expired int64
	err := q.QueryRowContext(ctx, `SELECT kind,root,started_at,ended_at,status,exit_code,input_digest,run_dir,report_dir,owner_pid,hostname,logs_expired FROM runs WHERE id=?`, id).Scan(&kind, &recordedRoot, &started, &ended, &status, &exit, &input, &runDir, &report, &pid, &host, &expired)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.Fail("RUNTIME", "未知运行 ID")
	}
	if err != nil {
		return nil, err
	}
	if recordedRoot != root || runDir != filepath.Join(dir, "runs", id) {
		return nil, domain.Fail("RUNTIME_OWNER", "运行身份或目录不匹配")
	}
	if _, err = safefs.Path(dir, "runs/"+id); err != nil {
		return nil, err
	}
	if expired != 0 && expired != 1 {
		return nil, domain.Fail("RUNTIME_SCHEMA", "运行清理状态非法")
	}
	stringValue := func(v sql.NullString) any {
		if v.Valid {
			return v.String
		}
		return nil
	}
	intValue := func(v sql.NullInt64) any {
		if v.Valid {
			return v.Int64
		}
		return nil
	}
	return map[string]any{
		"id": id, "kind": kind, "root": recordedRoot, "started_at": started,
		"ended_at": stringValue(ended), "status": status, "exit_code": intValue(exit),
		"input_digest": stringValue(input), "run_dir": runDir, "report_dir": stringValue(report),
		"owner_pid": intValue(pid), "hostname": stringValue(host), "logs_expired": expired != 0,
	}, nil
}

func runtimeQuery(ctx context.Context, action, root, dir, id string) (any, error) {
	db, err := openRuntime(ctx, dir, true)
	if err != nil {
		return nil, err
	}
	if db == nil {
		return nil, domain.Fail("RUNTIME", "运行数据库不存在；只读查询不会创建数据库")
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	run, err := runtimeRecord(ctx, tx, root, dir, id)
	if err != nil {
		return nil, err
	}
	result := map[string]any{
		"schema_version": 1, "read_only": true, "directory": dir, "database_exists": true,
		"id": id, "run": run, "scope": "runtime-records-only",
		"reference_scan": "UNPORTED", "lifecycle_approval": false, "execution_authorization": "not-evaluated",
	}
	switch action {
	case "events":
		result["events"], err = runtimeJSONRows(ctx, tx, id, true)
	case "commands":
		result["commands"], err = runtimeJSONRows(ctx, tx, id, false)
	case "pins":
		result["pins"], err = runtimePinRows(ctx, tx, id)
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func runtimeJSONRows(ctx context.Context, q runtimeQuerier, id string, events bool) ([]map[string]any, error) {
	query := "SELECT id,recorded_at,result_json FROM commands WHERE run_id=? ORDER BY id"
	if events {
		query = "SELECT id,recorded_at,type,value_json FROM events WHERE run_id=? ORDER BY id"
	}
	rows, err := q.QueryContext(ctx, query, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []map[string]any{}
	for rows.Next() {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		var rowID int64
		var recorded, typeName, raw string
		if events {
			err = rows.Scan(&rowID, &recorded, &typeName, &raw)
		} else {
			err = rows.Scan(&rowID, &recorded, &raw)
		}
		if err != nil {
			return nil, err
		}
		if !json.Valid([]byte(raw)) {
			return nil, domain.Fail("RUNTIME_DATA", "运行记录不是合法 JSON")
		}
		value, err := schema.Parse([]byte(raw))
		if err != nil {
			return nil, domain.Wrap("RUNTIME_DATA", err)
		}
		entry := map[string]any{"id": rowID, "recorded_at": recorded}
		if events {
			if strings.TrimSpace(typeName) == "" {
				return nil, domain.Fail("RUNTIME_DATA", "事件类型为空")
			}
			entry["type"], entry["value"] = typeName, value
		} else {
			if _, ok := object(value); !ok {
				return nil, domain.Fail("RUNTIME_DATA", "命令结果记录必须是 JSON 对象")
			}
			entry["result"] = value
		}
		result = append(result, entry)
	}
	return result, rows.Err()
}

func runtimePinRows(ctx context.Context, q runtimeQuerier, id string) ([]map[string]any, error) {
	rows, err := q.QueryContext(ctx, "SELECT reason,created_at FROM pins WHERE run_id=? ORDER BY reason", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []map[string]any{}
	for rows.Next() {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		var reason, created string
		if err = rows.Scan(&reason, &created); err != nil {
			return nil, err
		}
		if strings.TrimSpace(reason) == "" {
			return nil, domain.Fail("RUNTIME_DATA", "运行保护原因为空")
		}
		result = append(result, map[string]any{"reason": reason, "created_at": created})
	}
	return result, rows.Err()
}

// runtimePin extends the native owner protocol with an idempotent unpin. Neither
// action deletes run records, registered evidence, objects, or files, and ended
// runs remain protectable just as in the legacy pin contract.
func runtimePin(ctx context.Context, action, root, dir, id, token, reason string) (any, error) {
	if strings.TrimSpace(reason) == "" {
		return nil, domain.Fail("INPUT", action+" 需要非空 --reason")
	}
	if err := runtimeOwner(dir, id, token, root); err != nil {
		return nil, err
	}
	db, err := openRuntimeMode(ctx, dir, false, true)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	run, err := runtimeRecord(ctx, tx, root, dir, id)
	if err != nil {
		return nil, err
	}
	if run["logs_expired"] == true {
		return nil, domain.Fail("RUNTIME_STATE", "运行已进入清理状态，拒绝修改保护")
	}
	if err = runtimeOwner(dir, id, token, root); err != nil {
		return nil, err
	}
	var changed sql.Result
	if action == "pin" {
		changed, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO pins(run_id,reason,created_at) VALUES(?,?,?)", id, reason, time.Now().UTC().Format(time.RFC3339Nano))
	} else {
		changed, err = tx.ExecContext(ctx, "DELETE FROM pins WHERE run_id=? AND reason=?", id, reason)
	}
	if err != nil {
		return nil, err
	}
	n, err := changed.RowsAffected()
	if err != nil {
		return nil, err
	}
	pins, err := runtimePinRows(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return map[string]any{
		"schema_version": 1, "read_only": false, "id": id, "reason": reason,
		"action": action, "changed": n != 0, "idempotent": n == 0,
		"pins": pins, "protected": len(pins) != 0, "evidence_deleted": false,
		"scope": "runtime-records-only", "lifecycle_approval": false, "execution_authorization": "not-evaluated",
	}, nil
}
