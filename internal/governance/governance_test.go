package governance_test

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/governance"
)

const validContext = "---\ncontext_schema_version: 1\n---\n## 流程术语\n| 术语 | 含义 | 英文标识 | 避免 / 备注 |\n|---|---|---|---|\n| Spec | 规格 | — | |\n## 业务术语\n| 术语 | 含义 | 英文标识 | 适用业务责任区 | 避免 / 备注 |\n|---|---|---|---|---|\n| 报告 | 提交的报告 | Report | Reporting | 避免：表单 |\n"

func put(t *testing.T, root, ref, data string) {
	t.Helper()
	p := filepath.Join(root, ref)
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func canonicalTemp(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestContextRejectsNestedAuthorityAndGlobalRedefinition(t *testing.T) {
	root := canonicalTemp(t)
	put(t, root, "CONTEXT.md", validContext)
	put(t, root, "docs/CONTEXT.md", validContext)
	if _, err := governance.Run("context", "verify", root, nil); err == nil {
		t.Fatal("nested authority accepted")
	}
	if err := os.Remove(filepath.Join(root, "docs/CONTEXT.md")); err != nil {
		t.Fatal(err)
	}
	put(t, root, "CONTEXT.md", validContext+"| 通用报告 | 全局定义 | Report | Global | |\n")
	if _, err := governance.Run("context", "verify", root, nil); err == nil {
		t.Fatal("Global redefinition accepted")
	}
}

func TestContextDigestNormalizesLineEndingsAndQueriesStableIdentity(t *testing.T) {
	root := canonicalTemp(t)
	put(t, root, "CONTEXT.md", validContext)
	v, err := governance.Run("context", "verify", root, map[string]string{"term-refs": "Reporting/Report"})
	if err != nil {
		t.Fatal(err)
	}
	digest := v.(map[string]any)["document_digest"]
	put(t, root, "CONTEXT.md", strings.ReplaceAll(validContext, "\n", "  \r\n")+"\r\n")
	w, err := governance.Run("context", "verify", root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if w.(map[string]any)["document_digest"] != digest {
		t.Fatal("normalized digest drift")
	}
	q, err := governance.Run("context", "query", root, map[string]string{"id": "Reporting/Report"})
	if err != nil {
		t.Fatal(err)
	}
	if q.(map[string]any)["term"].(governance.Term).EnglishIdentifier != "Report" {
		t.Fatal("wrong term")
	}
	if _, err := governance.Run("context", "query", root, map[string]string{"id": "Reporting/Missing"}); err == nil {
		t.Fatal("unregistered identity accepted")
	}
}

func TestArchiveRejectsTraversalWithoutCreatingOutput(t *testing.T) {
	root := canonicalTemp(t)
	archive := filepath.Join(root, "unsafe.zip")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	entry, err := w.Create("../escape")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = entry.Write([]byte("unsafe")); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(canonicalTemp(t), "extract")
	if _, err = governance.Run("archive", "unpack", root, map[string]string{"source": "unsafe.zip", "output": dest}); err == nil {
		t.Fatal("traversal accepted")
	}
	if _, err = os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("rejected archive created output")
	}
}

func TestArchiveRoundtripPreservesBytesAndRejectsCaseFoldCollisions(t *testing.T) {
	root := canonicalTemp(t)
	put(t, root, "bundle/docs/说明.md", "业务证据\n")
	external := canonicalTemp(t)
	archive := filepath.Join(external, "bundle.zip")
	if _, err := governance.Run("archive", "pack", root, map[string]string{"source": "bundle", "output": archive}); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(archive, filepath.Join(root, "bundle.zip")); err != nil {
		t.Fatal(err)
	}
	extract := filepath.Join(external, "extracted")
	if _, err := governance.Run("archive", "unpack", root, map[string]string{"source": "bundle.zip", "output": extract}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(extract, "docs/说明.md"))
	if err != nil || string(b) != "业务证据\n" {
		t.Fatalf("roundtrip: %q %v", b, err)
	}
	f, err := os.Create(filepath.Join(root, "collision.zip"))
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for _, name := range []string{"straße", "STRASSE"} {
		e, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = e.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = governance.Run("archive", "verify", root, map[string]string{"source": "collision.zip"}); err == nil {
		t.Fatal("Unicode casefold collision accepted")
	}
}

func TestLifecycleQueriesRegisteredWorkUnitWithoutInferringStage(t *testing.T) {
	root := canonicalTemp(t)
	put(t, root, ".template-spec/process/lifecycle-registry.yaml", "schema_version: 1\nstages:\n  - id: stage.plan\n    name: Plan\nwork_units:\n  - id: work-unit.explore\n    owner: role.requirements-manager\n")
	v, err := governance.Run("lifecycle", "query", root, map[string]string{"id": "work-unit.explore"})
	if err != nil {
		t.Fatal(err)
	}
	m := v.(map[string]any)
	if m["execution_authorization"] != "not-evaluated" || m["read_only"] != true {
		t.Fatalf("query confers authority: %#v", m)
	}
	entry := m["entry"].(map[string]any)
	if _, ok := entry["stage"]; ok {
		t.Fatal("stage inferred")
	}
	if _, err = governance.Run("lifecycle", "query", root, map[string]string{"id": "work-unit.missing"}); err == nil {
		t.Fatal("unknown work unit accepted")
	}
}

func TestRuntimePersistsEventsAndRejectsDuplicateCompletion(t *testing.T) {
	root := canonicalTemp(t)
	home := canonicalTemp(t)
	v, err := governance.Run("runtime", "begin", root, map[string]string{"home": home, "kind": "test-command"})
	if err != nil {
		t.Fatal(err)
	}
	id := v.(map[string]any)["id"].(string)
	token := v.(map[string]any)["token"].(string)
	if _, err = governance.Run("runtime", "event", root, map[string]string{"home": home, "id": id, "token": token, "type": "verification", "value": "{\"exit_code\":0}"}); err != nil {
		t.Fatal(err)
	}
	if _, err = governance.Run("runtime", "complete", root, map[string]string{"home": home, "id": id, "token": token, "status": "passed", "exit-code": "0"}); err != nil {
		t.Fatal(err)
	}
	v, err = governance.Run("runtime", "inspect", root, map[string]string{"home": home})
	if err != nil {
		t.Fatal(err)
	}
	m := v.(map[string]any)
	runs := m["runs"].([]map[string]any)
	if len(runs) != 1 || runs[0]["status"] != "passed" || runs[0]["events"] != int64(1) {
		t.Fatalf("stored run: %#v", m)
	}
	if _, err = governance.Run("runtime", "complete", root, map[string]string{"home": home, "id": id, "token": token, "status": "passed", "exit-code": "0"}); err == nil {
		t.Fatal("duplicate completion accepted")
	}
	if _, err = governance.Run("runtime", "inspect", root, map[string]string{"home": filepath.Join(root, "runtime")}); err == nil {
		t.Fatal("runtime inside project accepted")
	}
}

func TestMavenXMLReadsProfilesAndModulesWithoutExecuting(t *testing.T) {
	root := canonicalTemp(t)
	put(t, root, "pom.xml", `<project xmlns="http://maven.apache.org/POM/4.0.0"><artifactId>report-service</artifactId><dependencies><dependency><artifactId>spring-web</artifactId></dependency></dependencies><profiles><profile><dependencies><dependency><artifactId>jdbc</artifactId></dependency></dependencies><modules><module>adapter</module></modules></profile></profiles><modules><module>domain</module></modules></project>`)
	v, err := governance.Run("xml", "inspect", root, map[string]string{"file": "pom.xml"})
	if err != nil {
		t.Fatal(err)
	}
	m := v.(map[string]any)
	if m["artifact_id"] != "report-service" {
		t.Fatalf("wrong artifact: %#v", m)
	}
	if deps := m["dependencies"].([]string); len(deps) != 2 || deps[0] != "spring-web" || deps[1] != "jdbc" {
		t.Fatalf("dependencies: %#v", deps)
	}
	if modules := m["modules"].([]string); len(modules) != 2 || modules[0] != "adapter" || modules[1] != "domain" {
		t.Fatalf("modules: %#v", modules)
	}
	put(t, root, "unsafe.xml", `<!DOCTYPE project [<!ENTITY remote SYSTEM "https://example.invalid/secret">]><project><artifactId>&remote;</artifactId></project>`)
	if _, err = governance.Run("xml", "inspect", root, map[string]string{"file": "unsafe.xml"}); err == nil {
		t.Fatal("DTD accepted")
	}
}

func TestContextSnapshotBindsExistingContractDigests(t *testing.T) {
	root := canonicalTemp(t)
	put(t, root, "CONTEXT.md", validContext)
	// Goldens come from the retained JS Context Contract implementation, not the Go implementation.
	put(t, root, "snapshot.json", `{"context_schema_version":1,"context_ref":"CONTEXT.md","term_refs":["Reporting/Report"],"document_digest":"sha256:28bec9d1649d9cf1503acc1e56ea4da3ca8a60ac9d03eda0e21f2ecd241947d0","referenced_terms_digest":"sha256:a7f531a29ec6502919ab893d6daf5d86e9f2afafb88e160f245e8e44b68fc7ca"}`)
	v, err := governance.Run("context", "verify", root, map[string]string{"snapshot": "snapshot.json"})
	if err != nil {
		t.Fatal(err)
	}
	if v.(map[string]any)["snapshot_validation"] != "passed" {
		t.Fatal("snapshot not checked")
	}
	put(t, root, "CONTEXT.md", strings.Replace(validContext, "提交的报告", "修订报告含义", 1))
	if _, err = governance.Run("context", "verify", root, map[string]string{"snapshot": "snapshot.json"}); err == nil {
		t.Fatal("stale snapshot accepted")
	}
}

func TestRuntimeUnknownSchemaAndMissingOwnerStayBlocked(t *testing.T) {
	root := canonicalTemp(t)
	home := canonicalTemp(t)
	v, err := governance.Run("runtime", "begin", root, map[string]string{"home": home})
	if err != nil {
		t.Fatal(err)
	}
	m := v.(map[string]any)
	if _, err = governance.Run("runtime", "complete", root, map[string]string{"home": home, "id": m["id"].(string), "status": "passed", "exit-code": "0"}); err == nil {
		t.Fatal("run completed without ownership token")
	}
	dbFile := filepath.Join(m["directory"].(string), "runtime.sqlite")
	db, err := sql.Open("sqlite", dbFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("PRAGMA user_version=999"); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(dbFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = governance.Run("runtime", "begin", root, map[string]string{"home": home}); err == nil {
		t.Fatal("unknown database schema accepted")
	}
	after, err := os.ReadFile(dbFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("unknown schema database was mutated")
	}
}

func TestRuntimeRefusesRegisteredExternalRepositoryAndInspectIsReadOnly(t *testing.T) {
	root := canonicalTemp(t)
	external := canonicalTemp(t)
	put(t, root, "docs/repositories.yaml", "local_worktree: '"+external+"'\n")
	if _, err := governance.Run("runtime", "begin", root, map[string]string{"home": filepath.Join(external, "runtime")}); err == nil {
		t.Fatal("registered repository used for runtime")
	}
	home := filepath.Join(canonicalTemp(t), "absent")
	v, err := governance.Run("runtime", "inspect", root, map[string]string{"home": home})
	if err != nil {
		t.Fatal(err)
	}
	if v.(map[string]any)["database_exists"] != false {
		t.Fatal("absent database reported as existing")
	}
	if _, err = os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("read-only inspect created runtime directory")
	}
}

func TestSchemaCheckDoesNotStandInForEvidenceApproval(t *testing.T) {
	root := canonicalTemp(t)
	put(t, root, "schema.json", `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","required":["exit_code"],"properties":{"exit_code":{"const":0}},"additionalProperties":false}`)
	put(t, root, "evidence.json", `{"exit_code":0}`)
	v, err := governance.Run("evidence", "check", root, map[string]string{"schema": "schema.json", "file": "evidence.json"})
	if err != nil {
		t.Fatal(err)
	}
	m := v.(map[string]any)
	if m["schema_validation"] != "passed" || m["semantic_validation"] != "UNPORTED" || m["approval_created"] != false {
		t.Fatalf("check claims approval: %#v", m)
	}
	if _, err = governance.Run("evidence", "verify", root, map[string]string{"schema": "schema.json", "file": "evidence.json"}); err == nil {
		t.Fatal("full evidence verify silently reduced to schema")
	}
	if _, err = governance.Run("evidence", "check", root, map[string]string{"schema": "schema.json", "file": "evidence.json", "require-approved": "true"}); err == nil {
		t.Fatal("approval flag ignored")
	}
	put(t, root, "evidence.json", `{"exit_code":1}`)
	if _, err = governance.Run("evidence", "check", root, map[string]string{"schema": "schema.json", "file": "evidence.json"}); err == nil {
		t.Fatal("failed execution evidence passes schema")
	}
}

func TestContextDigestPreservesUnicodeAndLiteralEscapeSequences(t *testing.T) {
	root := canonicalTemp(t)
	source := strings.Replace(validContext, "提交的报告", "\\u2028\u2028", 1)
	source = strings.Replace(source, "避免：表单", "避免：𠮷、表单", 1)
	put(t, root, "CONTEXT.md", source)
	v, err := governance.Run("context", "verify", root, map[string]string{"term-refs": "Reporting/Report"})
	if err != nil {
		t.Fatal(err)
	}
	if v.(map[string]any)["referenced_terms_digest"] != "sha256:0199d613b4760e91b620eb0b9aea4db25f54c692de35283bf50f3ea2c51f34bf" {
		t.Fatalf("referenced digest: %v", v.(map[string]any)["referenced_terms_digest"])
	}
}

func TestContextContractReadsTermsWithoutApproving(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	put(t, root, "CONTEXT.md", validContext)
	v, err := governance.Run("context", "check", root, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	m := v.(map[string]any)
	if m["process_terms"] != 1 || m["execution_authorization"] != "not-evaluated" {
		t.Fatalf("unexpected context result: %#v", m)
	}
	terms := m["business_terms"].([]governance.Term)
	if len(terms) != 1 || terms[0].TermRef != "Reporting/Report" || terms[0].ForbiddenAliases[0] != "表单" {
		t.Fatalf("terms: %#v", terms)
	}
}
