package governance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func runtimeQueryTemp(t *testing.T) string {
	t.Helper()
	p, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

type runtimeQueryFixture struct {
	root, home, dir, id, token, runDir string
}

func newRuntimeQueryFixture(t *testing.T) runtimeQueryFixture {
	t.Helper()
	f := runtimeQueryFixture{root: runtimeQueryTemp(t), home: runtimeQueryTemp(t)}
	v, err := runtimeRun(context.Background(), "begin", f.root, map[string]string{"home": f.home, "kind": "runtime-query-test"})
	if err != nil {
		t.Fatal(err)
	}
	m := v.(map[string]any)
	f.dir, f.id, f.token, f.runDir = m["directory"].(string), m["id"].(string), m["token"].(string), m["run_dir"].(string)
	return f
}

func (f runtimeQueryFixture) args() map[string]string {
	return map[string]string{"home": f.home, "id": f.id, "token": f.token}
}

func (f runtimeQueryFixture) database(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(f.dir, "runtime.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func runtimeQuerySnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(p string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		st, err := entry.Info()
		if err != nil {
			return err
		}
		value := st.Mode().String()
		if !entry.IsDir() {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			value += safefs.Digest(b)
		}
		result[p] = value
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func requireRuntimeQueryCode(t *testing.T, err error, code string) {
	t.Helper()
	var e *domain.Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("expected %s, got %v", code, err)
	}
}

func TestRuntimeQueriesReadRunningAndEndedRecordsWithoutChanges(t *testing.T) {
	f := newRuntimeQueryFixture(t)
	a := f.args()
	a["type"], a["value"] = "sample", `{"number":9007199254740993,"path":"../../do-not-read"}`
	if _, err := runtimeRun(context.Background(), "event", f.root, a); err != nil {
		t.Fatal(err)
	}
	db := f.database(t)
	if _, err := db.Exec("INSERT INTO commands(run_id,recorded_at,result_json) VALUES(?,?,?)", f.id, "2026-10-05T00:00:00Z", `{"stdoutFile":"/outside/evidence","exit_code":0}`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for _, ended := range []bool{false, true} {
		if ended {
			a = f.args()
			a["status"], a["exit-code"] = "passed", "0"
			if _, err := runtimeRun(context.Background(), "complete", f.root, a); err != nil {
				t.Fatal(err)
			}
		}
		before := runtimeQuerySnapshot(t, f.home)
		for _, action := range []string{"run", "events", "commands", "pins"} {
			v, err := runtimeRun(context.Background(), action, f.root, map[string]string{"home": f.home, "id": f.id})
			if err != nil {
				t.Fatal(action, err)
			}
			m := v.(map[string]any)
			if m["read_only"] != true || m["execution_authorization"] != "not-evaluated" || m["lifecycle_approval"] != false {
				t.Fatalf("query inferred approval: %#v", m)
			}
			run := m["run"].(map[string]any)
			if (run["ended_at"] != nil) != ended || run["root"] != f.root {
				t.Fatalf("wrong run: %#v", run)
			}
			if action == "events" {
				rows := m["events"].([]map[string]any)
				if len(rows) != 1 || rows[0]["value"].(map[string]any)["number"] != json.Number("9007199254740993") {
					t.Fatalf("event precision lost: %#v", rows)
				}
			}
			if action == "commands" && len(m["commands"].([]map[string]any)) != 1 {
				t.Fatal("command result missing")
			}
			if action == "pins" && len(m["pins"].([]map[string]any)) != 0 {
				t.Fatal("empty protection not represented as []")
			}
		}
		if after := runtimeQuerySnapshot(t, f.home); !reflect.DeepEqual(before, after) {
			t.Fatal("read-only queries changed database, evidence or sidecars")
		}
	}
}

func TestRuntimeQueriesMissingDatabaseAndExplicitIDRefusalsDoNotCreateFiles(t *testing.T) {
	root, home := runtimeQueryTemp(t), runtimeQueryTemp(t)
	before := runtimeQuerySnapshot(t, home)
	for _, action := range []string{"run", "events", "commands", "pins"} {
		_, err := runtimeRun(context.Background(), action, root, map[string]string{"home": home, "id": "missing-run"})
		requireRuntimeQueryCode(t, err, "RUNTIME")
	}
	for _, a := range []map[string]string{
		{"home": home}, {"home": home, "id": "../escape"}, {"home": home, "id": "one' OR 1=1--"},
		{"home": home, "id": "a/b"}, {"home": home, "id": "a\\b"}, {"home": home, "id": "a", "arg0": "b"},
		{"home": home, "id": strings.Repeat("a", 121)},
	} {
		_, err := runtimeRun(context.Background(), "run", root, a)
		requireRuntimeQueryCode(t, err, "INPUT")
	}
	if !reflect.DeepEqual(before, runtimeQuerySnapshot(t, home)) {
		t.Fatal("missing or refused queries created files")
	}
	for _, action := range []string{"run", "events", "commands", "pins"} {
		_, err := runtimeRun(context.Background(), action, root, map[string]string{"home": filepath.Join(root, "runtime"), "id": "missing-run"})
		requireRuntimeQueryCode(t, err, "PATH")
	}
}

func TestRuntimePinUnpinAreIdempotentAfterCompletionAndPreserveEvidence(t *testing.T) {
	f := newRuntimeQueryFixture(t)
	a := f.args()
	a["status"], a["exit-code"] = "completed", "0"
	if _, err := runtimeRun(context.Background(), "complete", f.root, a); err != nil {
		t.Fatal(err)
	}
	evidence := filepath.Join(f.runDir, "report.json")
	if err := os.WriteFile(evidence, []byte(`{"approved":false}`), 0600); err != nil {
		t.Fatal(err)
	}
	beforeEvidence := runtimeQuerySnapshot(t, f.runDir)
	db := f.database(t)
	if _, err := db.Exec("INSERT INTO run_files(run_id,path,sha256,bytes) VALUES(?,?,?,?)", f.id, evidence, safefs.Digest([]byte(`{"approved":false}`)), 18); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	a = f.args()
	a["reason"] = "human-review-evidence"
	var firstCreated any
	for i, action := range []string{"pin", "pin", "unpin", "unpin"} {
		v, err := runtimeRun(context.Background(), action, f.root, a)
		if err != nil {
			t.Fatal(action, err)
		}
		m := v.(map[string]any)
		if m["changed"] != (i%2 == 0) || m["idempotent"] != (i%2 == 1) || m["evidence_deleted"] != false {
			t.Fatalf("wrong idempotence: %#v", m)
		}
		pins := m["pins"].([]map[string]any)
		if i == 0 {
			firstCreated = pins[0]["created_at"]
		}
		if i == 1 && pins[0]["created_at"] != firstCreated {
			t.Fatal("duplicate pin changed original creation time")
		}
	}
	if !reflect.DeepEqual(beforeEvidence, runtimeQuerySnapshot(t, f.runDir)) {
		t.Fatal("unpin deleted or changed evidence/owner files")
	}
	db = f.database(t)
	var count int
	if err := db.QueryRow("SELECT count(*) FROM run_files WHERE run_id=?", f.id).Scan(&count); err != nil || count != 1 {
		t.Fatalf("unpin removed evidence registration: %d, %v", count, err)
	}
	if err := db.QueryRow("SELECT count(*) FROM runs WHERE id=? AND status='completed'", f.id).Scan(&count); err != nil || count != 1 {
		t.Fatalf("unpin removed/completed run differently: %d, %v", count, err)
	}
}

func TestRuntimeProtectionRejectsWrongOwnerOldRunAndExpiredState(t *testing.T) {
	f := newRuntimeQueryFixture(t)
	a := f.args()
	a["reason"], a["token"] = "evidence", strings.Repeat("0", 64)
	before := runtimeQuerySnapshot(t, f.home)
	for _, action := range []string{"pin", "unpin"} {
		_, err := runtimeRun(context.Background(), action, f.root, a)
		requireRuntimeQueryCode(t, err, "RUNTIME_OWNER")
	}
	if !reflect.DeepEqual(before, runtimeQuerySnapshot(t, f.home)) {
		t.Fatal("wrong token changed files")
	}
	a = f.args()
	a["reason"] = " "
	_, err := runtimeRun(context.Background(), "pin", f.root, a)
	requireRuntimeQueryCode(t, err, "INPUT")
	a["reason"] = "evidence"
	db := f.database(t)
	if _, err = db.Exec("UPDATE runs SET logs_expired=1 WHERE id=?", f.id); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	for _, action := range []string{"pin", "unpin"} {
		_, err = runtimeRun(context.Background(), action, f.root, a)
		requireRuntimeQueryCode(t, err, "RUNTIME_STATE")
	}
	if err = os.Remove(filepath.Join(f.runDir, ".go-owner.json")); err != nil {
		t.Fatal(err)
	}
	if _, err = runtimeRun(context.Background(), "run", f.root, map[string]string{"home": f.home, "id": f.id}); err != nil {
		t.Fatal("legacy ownership-less record cannot be read", err)
	}
	for _, action := range []string{"pin", "unpin"} {
		_, err = runtimeRun(context.Background(), action, f.root, a)
		requireRuntimeQueryCode(t, err, "RUNTIME_OWNER")
	}
}

func TestRuntimeQueryRejectsWrongDatabaseIdentityAndMalformedJSON(t *testing.T) {
	for _, field := range []string{"root", "run_dir"} {
		t.Run(field, func(t *testing.T) {
			f := newRuntimeQueryFixture(t)
			db := f.database(t)
			query := "UPDATE runs SET root=? WHERE id=?"
			if field == "run_dir" {
				query = "UPDATE runs SET run_dir=? WHERE id=?"
			}
			if _, err := db.Exec(query, "/other-project/../../outside", f.id); err != nil {
				t.Fatal(err)
			}
			_ = db.Close()
			for _, action := range []string{"run", "events", "commands", "pins", "pin", "unpin"} {
				a := f.args()
				a["reason"] = "evidence"
				_, err := runtimeRun(context.Background(), action, f.root, a)
				requireRuntimeQueryCode(t, err, "RUNTIME_OWNER")
			}
		})
	}
	for _, raw := range []string{`{"a":1,"a":2}`, `{"a":"\ud800"}`, `a: yaml-is-not-json`, `null`, `[]`} {
		t.Run(raw, func(t *testing.T) {
			f := newRuntimeQueryFixture(t)
			db := f.database(t)
			if _, err := db.Exec("INSERT INTO commands(run_id,recorded_at,result_json) VALUES(?,?,?)", f.id, "now", raw); err != nil {
				t.Fatal(err)
			}
			_ = db.Close()
			before := runtimeQuerySnapshot(t, f.home)
			_, err := runtimeRun(context.Background(), "commands", f.root, map[string]string{"home": f.home, "id": f.id})
			requireRuntimeQueryCode(t, err, "RUNTIME_DATA")
			if !reflect.DeepEqual(before, runtimeQuerySnapshot(t, f.home)) {
				t.Fatal("bad command JSON query changed files")
			}
		})
	}
}

func TestRuntimeQueriesUnknownRunAndOwnerRecordCannotBeRebound(t *testing.T) {
	f := newRuntimeQueryFixture(t)
	for _, action := range []string{"run", "events", "commands", "pins"} {
		_, err := runtimeRun(context.Background(), action, f.root, map[string]string{"home": f.home, "id": "unknown"})
		requireRuntimeQueryCode(t, err, "RUNTIME")
	}
	ownerPath := filepath.Join(f.runDir, ".go-owner.json")
	for _, raw := range []string{
		`{"version":1,"root":"/other-project","token_digest":"` + safefs.Digest([]byte(f.token)) + `"}`,
		`{"version":1,"version":1,"root":"` + filepath.ToSlash(f.root) + `","token_digest":"` + safefs.Digest([]byte(f.token)) + `"}`,
	} {
		if err := os.WriteFile(ownerPath, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		a := f.args()
		a["reason"] = "evidence"
		_, err := runtimeRun(context.Background(), "pin", f.root, a)
		requireRuntimeQueryCode(t, err, "RUNTIME_OWNER")
	}
}

func TestRuntimeReadOnlyWALQueryRefusesBeforeCreatingSidecars(t *testing.T) {
	f := newRuntimeQueryFixture(t)
	db := f.database(t)
	if _, err := db.Exec("UPDATE runs SET logs_expired=1 WHERE id=?", f.id); err != nil {
		t.Fatal(err)
	}
	var mode string
	if err := db.QueryRow("PRAGMA journal_mode=WAL").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("WAL fixture: %s %v", mode, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before := runtimeQuerySnapshot(t, f.home)
	for _, action := range []string{"inspect", "run", "events", "commands", "pins", "pin", "unpin"} {
		a := f.args()
		a["reason"] = "expired-evidence-must-remain-unchanged"
		_, err := runtimeRun(context.Background(), action, f.root, a)
		requireRuntimeQueryCode(t, err, "UNPORTED")
	}
	if !reflect.DeepEqual(before, runtimeQuerySnapshot(t, f.home)) {
		t.Fatal("read-only WAL attempt changed database/sidecar files")
	}
}

func TestRuntimeQueriesAndProtectionRespectCancellation(t *testing.T) {
	f := newRuntimeQueryFixture(t)
	before := runtimeQuerySnapshot(t, f.home)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, action := range []string{"run", "events", "commands", "pins", "pin", "unpin"} {
		a := f.args()
		a["reason"] = "evidence"
		_, err := runtimeRun(ctx, action, f.root, a)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled %s: %v", action, err)
		}
	}
	if !reflect.DeepEqual(before, runtimeQuerySnapshot(t, f.home)) {
		t.Fatal("cancelled query/mutation changed files")
	}
	for _, action := range []string{"run", "pin", "unpin"} {
		t.Run(action, func(t *testing.T) {
			db := f.database(t)
			if _, err := db.Exec("BEGIN EXCLUSIVE"); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				a := f.args()
				a["reason"] = "never-commit-cancelled-pin"
				_, err := runtimeRun(ctx, action, f.root, a)
				done <- err
			}()
			select {
			case err := <-done:
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("SQL cancellation did not propagate: %v", err)
				}
			case <-time.After(2 * time.Second):
				_, _ = db.Exec("ROLLBACK")
				t.Fatal("SQL query/mutation did not stop after cancellation")
			}
			// Keep the competing lock until the native request has stopped: ending
			// this lock earlier would conceal a non-cancellable busy handler.
			if _, err := db.Exec("ROLLBACK"); err != nil {
				t.Fatal(err)
			}
			var count int
			if err := db.QueryRow("SELECT count(*) FROM pins WHERE run_id=?", f.id).Scan(&count); err != nil || count != 0 {
				t.Fatalf("cancelled mutation persisted: %d %v", count, err)
			}
			_ = db.Close()
		})
	}
}

func TestRuntimeBusyIsExplicitAndReturnsWhileCompetingLockRemainsHeld(t *testing.T) {
	for _, action := range []string{"run", "pin", "unpin"} {
		t.Run(action, func(t *testing.T) {
			f := newRuntimeQueryFixture(t)
			db := f.database(t)
			if _, err := db.Exec("BEGIN EXCLUSIVE"); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				a := f.args()
				a["reason"] = "busy-evidence-not-written"
				_, err := runtimeRun(context.Background(), action, f.root, a)
				done <- err
			}()
			select {
			case err := <-done:
				requireRuntimeQueryCode(t, err, "RUNTIME_BUSY")
			case <-time.After(2 * time.Second):
				_, _ = db.Exec("ROLLBACK")
				t.Fatal("busy request did not stop while competing lock remained held")
			}
			if _, err := db.Exec("ROLLBACK"); err != nil {
				t.Fatal(err)
			}
			var count int
			if err := db.QueryRow("SELECT count(*) FROM pins WHERE run_id=?", f.id).Scan(&count); err != nil || count != 0 {
				t.Fatalf("busy mutation persisted: %d %v", count, err)
			}
		})
	}
}

func TestRuntimeExistingOwnerDoesNotAuthorizeDatabaseInitializationOrRepair(t *testing.T) {
	for _, fixture := range []string{"empty", "unknown-schema", "damaged"} {
		t.Run(fixture, func(t *testing.T) {
			f := newRuntimeQueryFixture(t)
			file := filepath.Join(f.dir, "runtime.sqlite")
			switch fixture {
			case "empty":
				if err := os.WriteFile(file, nil, 0600); err != nil {
					t.Fatal(err)
				}
			case "unknown-schema":
				db := f.database(t)
				if _, err := db.Exec("PRAGMA user_version=42"); err != nil {
					t.Fatal(err)
				}
				_ = db.Close()
			case "damaged":
				if err := os.WriteFile(file, []byte("not-a-database"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before := runtimeQuerySnapshot(t, f.home)
			for _, action := range []string{"pin", "unpin", "event", "complete"} {
				a := f.args()
				a["reason"], a["type"], a["status"], a["exit-code"] = "owned-evidence", "sample", "passed", "0"
				_, err := runtimeRun(context.Background(), action, f.root, a)
				if err == nil {
					t.Fatalf("%s repaired/initialized %s database using an owner token", action, fixture)
				}
				if fixture != "damaged" {
					requireRuntimeQueryCode(t, err, "RUNTIME_SCHEMA")
				}
				if !reflect.DeepEqual(before, runtimeQuerySnapshot(t, f.home)) {
					t.Fatalf("%s changed a refused %s database/sidecars", action, fixture)
				}
			}
		})
	}
}
