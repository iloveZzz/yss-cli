package governance

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

type backendFixture struct {
	root, project, head string
	binding, baseline   map[string]any
}

func backendTestGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "core.autocrlf=false"}, args...)...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=")
	b, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("git %v: %v %s", args, e, b)
	}
	return strings.TrimSpace(string(b))
}

func backendTestDirectoryZIP(t *testing.T, root, prefix, target string) {
	t.Helper()
	var body bytes.Buffer
	z := zip.NewWriter(&body)
	dir := filepath.Join(root, filepath.FromSlash(prefix))
	e := filepath.WalkDir(dir, func(file string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			t.Fatalf("synthetic package contains a non-regular file: %s", file)
		}
		ref, err := filepath.Rel(dir, file)
		if err != nil {
			return err
		}
		header := &zip.FileHeader{Name: filepath.ToSlash(ref), Method: zip.Deflate}
		header.SetMode(info.Mode())
		w, err := z.CreateHeader(header)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		_, err = w.Write(raw)
		return err
	})
	if e != nil {
		t.Fatal(e)
	}
	if e = z.Close(); e != nil {
		t.Fatal(e)
	}
	apTestPut(t, root, target, body.Bytes())
}
func backendTestFixture(t *testing.T) *backendFixture {
	t.Helper()
	root := apTestRoot(t)
	project := filepath.Join(root, "project")
	if e := os.MkdirAll(project, 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.Remove(filepath.Join(root, approvalSkillsRef)); e != nil {
		t.Fatal(e)
	}
	apTestPut(t, root, "project/pom.xml", "<project><modelVersion>4.0.0</modelVersion><artifactId>synthetic</artifactId></project>")
	apTestPut(t, root, "project/src/main/java/web/Endpoint.java", "package web; @RestController public class Endpoint { app.Service service; public String login(){return service.login();} }")
	apTestPut(t, root, "project/src/main/java/app/Service.java", "package app; public class Service { public String login(){return \"ok\";} }")
	apTestPut(t, root, "project/src/test/java/Test.java", "package test; class Test {}")
	backendTestGit(t, project, "init", "-q")
	backendTestGit(t, project, "config", "user.name", "Synthetic fixture")
	backendTestGit(t, project, "config", "user.email", "synthetic@example.invalid")
	backendTestGit(t, project, "add", ".")
	backendTestGit(t, project, "commit", "-qm", "Synthetic protocol baseline")
	baseline := map[string]any{"architecture_identity": map[string]any{"architecture_family": "domain-driven", "architecture_profile": "existing-domain-driven-maven", "platform_configuration": map[string]any{"component_platform_line": "boot2-java8", "java_version": 8}}, "source": map[string]any{"roots": []any{"."}}, "build_units": []any{map[string]any{"id": "business", "role_paths": map[string]any{"web": []any{"src/main/java/web"}, "application": []any{"src/main/java/app"}, "persistence": []any{"src/main/java/store"}, "domain": []any{"src/main/java/model"}}}}}
	b := apTestPut(t, root, "baseline.json", baseline)
	binding := map[string]any{"ref": "baseline.json", "digest": safefs.Digest(b)}
	lock := map[string]any{}
	s := newSemanticSession(context.Background(), root, nil)
	for _, skill := range append(append([]string{}, backendCoreSkills...), "alibaba-java-code-style", "yss-validation", "yss-exception", "mapstruct", "lombok") {
		apTestPut(t, root, ".agents/skills/"+skill+"/SKILL.md", "# Synthetic standard\n<!-- yss-rule {\"id\":\""+skill+".ownership\",\"when\":\"always\",\"level\":\"mandatory\"} -->\n<a id=\""+skill+".ownership\"></a>\nKeep the owning responsibility explicit.\n")
		hash, e := backendSkillTree(s, skill)
		if e != nil {
			t.Fatal(e)
		}
		lock[skill] = map[string]any{"effectiveHash": hash}
	}
	apTestPut(t, root, "skills-lock.json", map[string]any{"version": 3, "skills": map[string]any{"shared": lock}})
	apTestPut(t, root, "checks.log", "Synthetic protocol evidence only, not certification.")
	return &backendFixture{root: root, project: project, head: backendTestGit(t, project, "rev-parse", "HEAD"), binding: binding, baseline: baseline}
}
func (f *backendFixture) session(t *testing.T) *semanticSession {
	t.Helper()
	s := newSemanticSession(context.Background(), f.root, nil)
	if e := s.registerExternalRoot(f.project, "baseline.json"); e != nil {
		t.Fatal(e)
	}
	return s
}
func (f *backendFixture) compile(t *testing.T, s *semanticSession, contract map[string]any, kind string) map[string]any {
	t.Helper()
	comparison := ""
	if kind == "change" {
		comparison = f.head
	}
	d, e := backendCompileCoverage(s, f.project, contract, kind, f.binding, comparison, []string{}, []map[string]any{})
	if e != nil {
		t.Fatal(e)
	}
	return d
}
func (f *backendFixture) rows(t *testing.T, c map[string]any) []any {
	t.Helper()
	evidence := safefs.Digest([]byte("Synthetic protocol evidence only, not certification."))
	rows := []any{}
	for _, r := range apRows(c["constraints"]) {
		status := "passed"
		if r["applicability"] != "required" {
			status = "not-applicable"
		}
		rows = append(rows, map[string]any{"axis": "Standards", "skill": r["skill"], "constraint_id": r["constraint_id"], "constraint": "Synthetic owning rule was checked", "status": status, "reason": "No such conditional behavior exists in this synthetic fixture", "applicability_basis": r["applicability_basis"], "rule_ref": r["rule_ref"], "rule_digest": r["rule_digest"], "code_ref": "src/main/java/web/Endpoint.java:1", "evidence_ref": "checks.log", "evidence_digest": evidence, "review_notes": "Synthetic full text reasoning checks exact ownership and observable seams; this is not real approval."})
	}
	return rows
}

func TestBackendCoverageOracleAndActualDrift(t *testing.T) {
	f := backendTestFixture(t)
	s := f.session(t)
	c := f.compile(t, s, nil, "baseline")
	if len(apArray(c["issues"])) > 0 || len(apArray(c["findings"])) > 0 {
		t.Fatalf("unexpected gaps: %s", apCanonical(c))
	}
	if e := backendCoverageRows(s, f.project, c, f.rows(t, c)); e != nil {
		t.Fatal(e)
	}
	if e := s.finish(); e != nil {
		t.Fatal(e)
	}
	if legacy := os.Getenv("YSS_LEGACY_ORACLE_ROOT"); legacy != "" {
		script := `const {compileStandardsCoverage}=await import(process.argv[1]);const fs=await import('node:fs');const root=process.argv[2];console.log(JSON.stringify(compileStandardsCoverage({root,projectRoot:process.argv[3],baseline_binding:JSON.parse(fs.readFileSync(root+'/binding.json','utf8'))})));`
		apTestPut(t, f.root, "binding.json", f.binding)
		cmd := exec.Command("node", "--input-type=module", "-e", script, governanceOracleURL(filepath.Join(legacy, "scripts/lib/backend-standards-coverage.mjs")), f.root, f.project)
		b, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatalf("fixed source oracle: %v %s", e, b)
		}
		var old map[string]any
		if e = json.Unmarshal(b, &old); e != nil {
			t.Fatal(e)
		}
		if !apEqual(c, old) {
			t.Fatalf("coverage mismatch\nnative: %s\nlegacy: %s", apCanonical(c), apCanonical(old))
		}
	}
	apTestPut(t, f.root, "project/src/main/java/web/Endpoint.java", "package web; @RestController class Endpoint { store.SecretRepository repo; }")
	changed := f.compile(t, f.session(t), nil, "baseline")
	if len(apArray(changed["findings"])) == 0 {
		t.Fatal("direct persistence leak accepted")
	}
	if e := backendCoverageRows(f.session(t), f.project, changed, f.rows(t, changed)); e == nil {
		t.Fatal("violation accepted")
	}
	if e := s.finish(); e == nil {
		t.Fatal("source change after validation accepted")
	}
}
func TestBackendCoverageRowsRejectMissingDuplicateWaiverAndEvidence(t *testing.T) {
	for _, name := range []string{"missing", "duplicate", "waiver", "notes", "evidence"} {
		t.Run(name, func(t *testing.T) {
			f := backendTestFixture(t)
			s := f.session(t)
			c := f.compile(t, s, nil, "baseline")
			rows := f.rows(t, c)
			switch name {
			case "missing":
				rows = rows[1:]
			case "duplicate":
				rows = append(rows, rows[0])
			case "waiver":
				apMap(rows[0])["status"] = "not-applicable"
			case "notes":
				for _, row := range rows {
					if strings.HasSuffix(apText(apMap(row)["constraint_id"]), ".full-text") {
						apMap(row)["review_notes"] = "passed"
						break
					}
				}
			case "evidence":
				apTestPut(t, f.root, "checks.log", "manual changed evidence")
			}
			if e := backendCoverageRows(s, f.project, c, rows); e == nil {
				t.Fatal("invalid Standards review accepted")
			}
		})
	}
}
func TestBackendCoverageDiscoveryAndClosure(t *testing.T) {
	f := backendTestFixture(t)
	contract := map[string]any{"resolution": map[string]any{"architecture_identity": f.baseline["architecture_identity"], "required_skills": []any{"yss-web-controller", "yss-dto", "yss-application"}}, "common": map[string]any{}, "backend": map[string]any{}}
	apTestPut(t, f.root, "project/src/main/java/app/Service.java", "package app; public class Service { public String login(){return \"updated\";} }")
	c := f.compile(t, f.session(t), contract, "change")
	if !apContains(c["reviewed_paths"], "src/main/java/web/Endpoint.java") {
		t.Fatal("dependent HTTP boundary omitted")
	}
	apTestPut(t, f.root, "project/src/main/java/app/Service.java", "package app; @UnknownExternal class Service {}")
	c = f.compile(t, f.session(t), nil, "baseline")
	found := false
	for _, issue := range apRows(c["issues"]) {
		found = found || strings.HasPrefix(apText(issue["id"]), "unresolved-responsibility:")
	}
	if !found {
		t.Fatal("unknown annotation silently waived")
	}
	apTestPut(t, f.root, "project/src/main/resources/mapper.xml", "<mapper namespace=\"store.Data\"><select id=\"lookup\">SELECT 1</select></mapper>")
	c = f.compile(t, f.session(t), nil, "baseline")
	found = false
	for _, skill := range apRows(c["skills"]) {
		found = found || skill["skill"] == "yss-mybatis"
	}
	if !found {
		t.Fatal("MyBatis XML discovery missing")
	}
}

func TestBackendPublishedSourcePolicyRetainsCurrentBinding(t *testing.T) {
	for _, name := range []string{"source-v1", "scope", "basis-bytes", "reviewer", "unpublished", "v2-current-registry", "source-policy-drift", "missing-user-policy", "unknown-user-policy", "malformed-user-gates", "malformed-work-units", "missing-gate-policy", "unknown-unlisted-policy", "malformed-signing-bucket"} {
		t.Run(name, func(t *testing.T) {
			root := apTestRoot(t)
			record, state, gate := apTestApproval(t, root)
			binding := map[string]any{"ref": "subject.json", "digest": record["subject_digest"], "approval_ref": "approval.json", "approval_context": state, "review_package": false}
			s := apTestSession(t, root)
			// The consumer has retired this name, while the captured source
			// policy still explicitly publishes it for a version-one artifact.
			s.registry = apTestJSONClone(t, s.registry)
			apMap(s.registry["id_policy"])["deprecated_ids"] = []any{gate}
			if e := contractApproval(s, gate, binding, "approval.json", nil); e == nil {
				t.Fatal("generic current approval ignored the consumer's deprecated gate")
			}
			switch name {
			case "scope":
				record["approval_scope"] = []any{"other.unit"}
			case "basis-bytes":
				apTestPut(t, root, "basis.json", map[string]any{"test_only": true, "version": "changed"})
			case "reviewer":
				record["principal_ref"] = record["drafter_principal_ref"]
			case "unpublished":
				s.roles = apTestJSONClone(t, s.roles)
				policy := apMap(s.roles["gate_policy"])
				rows := []any{}
				for _, row := range apRows(policy["check_reviews"]) {
					if row["gate"] != gate {
						rows = append(rows, row)
					}
				}
				policy["check_reviews"] = rows
			case "v2-current-registry":
				record["schema_version"] = 2
			case "missing-user-policy", "unknown-user-policy", "malformed-user-gates", "malformed-work-units", "missing-gate-policy", "unknown-unlisted-policy", "malformed-signing-bucket":
				s.roles = apTestJSONClone(t, s.roles)
				switch name {
				case "missing-user-policy":
					delete(s.roles, "user_decision_policy")
				case "unknown-user-policy":
					apMap(s.roles["user_decision_policy"])["schema_version"] = 99
				case "malformed-user-gates":
					apMap(s.roles["user_decision_policy"])["gates"] = "none"
				case "malformed-work-units":
					apMap(s.roles["user_decision_policy"])["work_units"] = []any{}
				case "missing-gate-policy":
					delete(s.roles, "gate_policy")
				case "unknown-unlisted-policy":
					apMap(s.roles["gate_policy"])["default_if_unlisted"] = "accept"
				case "malformed-signing-bucket":
					apMap(s.roles["gate_policy"])["digital_human_review"] = "disabled"
				}
			}
			if name != "source-v1" && name != "source-policy-drift" {
				apTestPut(t, root, "approval.json", record)
			}
			e := backendArtifactApproval(s, gate, binding, record, nil)
			positive := name == "source-v1" || name == "source-policy-drift"
			if positive && e != nil {
				t.Fatal(e)
			}
			if !positive && e == nil {
				t.Fatal("source publication bypassed current evidence, independent reviewer or schema-two authority")
			}
			if name == "source-policy-drift" {
				apTestPut(t, root, approvalRolesRef, map[string]any{"changed_after_check": true})
				if e = s.finish(); e == nil {
					t.Fatal("source policy changed after validation without invalidating the result")
				}
			}
		})
	}
}

func TestBackendSourceV2FreezesActualAuthorityBytes(t *testing.T) {
	for _, name := range []string{"complete", "omit-lifecycle", "omit-skills", "wrong-digest", "wrong-ref", "actual-file-missing", "lifecycle-version", "lifecycle-inactive", "skills-version", "skills-inactive", "roles-version", "roles-inactive"} {
		t.Run(name, func(t *testing.T) {
			root := apTestRoot(t)
			task, record, expected, gate := apTestFormalReview(t, root)
			// Mutate the actual source authority before its byte binding is frozen,
			// so a valid digest cannot disguise an unsupported policy version/state.
			for _, authority := range []struct{ prefix, ref string }{{"lifecycle", approvalRegistryRef}, {"skills", approvalSkillsRef}, {"roles", approvalRolesRef}} {
				if strings.HasPrefix(name, authority.prefix+"-") {
					source := newSemanticSession(context.Background(), root, nil)
					doc, e := source.doc(authority.ref)
					if e != nil {
						t.Fatal(e)
					}
					if strings.HasSuffix(name, "-version") {
						doc["schema_version"] = 99
					} else {
						doc["status"] = "inactive"
					}
					apTestPut(t, root, authority.ref, doc)
				}
			}
			rc := apCopy(apMap(task["review_context"]))
			policyRaw, e := os.ReadFile(filepath.Join(root, filepath.FromSlash(approvalRolesRef)))
			if e != nil {
				t.Fatal(e)
			}
			rc["policy_digest"] = safefs.Digest(policyRaw)
			basis := append([]any{}, apArray(rc["basis"])...)
			for _, ref := range []string{approvalRegistryRef, approvalSkillsRef} {
				if name == "omit-lifecycle" && ref == approvalRegistryRef || name == "omit-skills" && ref == approvalSkillsRef {
					continue
				}
				raw, e := os.ReadFile(filepath.Join(root, filepath.FromSlash(ref)))
				if e != nil {
					t.Fatal(e)
				}
				row := map[string]any{"ref": ref, "digest": safefs.Digest(raw)}
				if ref == approvalRegistryRef && name == "wrong-digest" {
					row["digest"] = strings.Repeat("f", 64)
				}
				if ref == approvalRegistryRef && name == "wrong-ref" {
					apTestPut(t, root, "consumer-registry-copy.yaml", raw)
					row["ref"] = "consumer-registry-copy.yaml"
				}
				basis = append(basis, row)
			}
			rc["basis"] = basis
			task["review_context"] = rc
			task["inputs"] = append(apArray(task["inputs"]), approvalRegistryRef, approvalSkillsRef)
			taskDigest := safefs.Digest(apTestPut(t, root, "formal-review-task.json", task))
			record["review_task_digest"] = taskDigest
			apTestPut(t, root, "review-approval.json", record)
			consumer := apCopy(rc)
			consumer["review_task_ref"] = "formal-review-task.json"
			consumer["review_task_digest"] = taskDigest
			expected["review_context"] = consumer
			binding := map[string]any{"ref": record["subject_ref"], "digest": "sha256:" + apText(record["subject_digest"]), "approval_ref": "review-approval.json", "approval_context": expected, "review_package": false}
			apTestPut(t, root, "source-v2-binding.json", binding)
			// This is a captured source view, not the receiving main authority load.
			// The backend adapter itself must enforce its actual policy versions.
			s := newSemanticSession(context.Background(), root, nil)
			if name == "actual-file-missing" {
				if e := os.Remove(filepath.Join(root, filepath.FromSlash(approvalSkillsRef))); e != nil {
					t.Fatal(e)
				}
			}
			e = backendArtifactApproval(s, gate, binding, record, nil)
			if (e == nil) != (name == "complete") {
				t.Fatalf("source v2 %s accepted=%v: %v", name, e == nil, e)
			}
			if e == nil {
				if e = s.finish(); e != nil {
					t.Fatal(e)
				}
			}
			strict := name == "lifecycle-version" || name == "lifecycle-inactive"
			if legacy := os.Getenv("YSS_LEGACY_ORACLE_ROOT"); legacy != "" && (name == "complete" || strings.HasPrefix(name, "omit-") || strict) {
				script := `const fs=await import('node:fs');const path=await import('node:path');const {sourceApproval}=await import(process.argv[1]);const {parseDocument}=await import(process.argv[2]);const root=process.argv[3],json=ref=>JSON.parse(fs.readFileSync(path.join(root,ref),'utf8'));try{await sourceApproval(json('review-approval.json'),parseDocument(fs.readFileSync(path.join(root,'.template-spec/agents/digital-human-roles.yaml'),'utf8')).toJS(),root,json('source-v2-binding.json'));console.log('source v2 approval accepted');}catch(error){console.error(error.message);process.exitCode=1;}`
				cmd := exec.Command("node", "--input-type=module", "-e", script, governanceOracleURL(filepath.Join(legacy, "scripts/lib/strategic-handoff.mjs")), governanceOracleURL(filepath.Join(legacy, "scripts/vendor/yaml.mjs")), root)
				out, err := cmd.CombinedOutput()
				if (err == nil) != (name == "complete" || strict) {
					t.Fatalf("fixed source v2 %s accepted=%v: %v %s", name, err == nil, err, out)
				}
				t.Logf("fixed-source-v2-authority case=%s legacy_accepted=%v native_accepted=%v raw=%s", name, err == nil, e == nil, out)
				if strict {
					t.Logf("intentional-strict-difference source-v2-%s legacy_accepted=true native_accepted=false", name)
				}
			}
		})
	}
}

func TestBackendReviewBaselineReadOnlyAndMachineEvidence(t *testing.T) {
	f := backendTestFixture(t)
	coverage := f.compile(t, f.session(t), nil, "baseline")
	raw := apTestPut(t, f.root, "coverage.json", coverage)
	candidate := backendCoverageDigest(coverage["inventory"])
	input := map[string]any{"scope_kind": "baseline", "project_root": f.project, "baseline_binding": f.binding, "standards_coverage_ref": "coverage.json", "standards_coverage_digest": safefs.Digest(raw), "candidate_digest": candidate, "implementation_actor_id": "synthetic-worker", "implementation_instance_id": "worker-instance"}
	check := map[string]any{"command": "synthetic mechanism evidence", "exit_code": 0, "executed_at": time.Now().UTC().Format(time.RFC3339Nano), "candidate_digest": candidate, "evidence_ref": "checks.log", "evidence_digest": safefs.Digest([]byte("Synthetic protocol evidence only, not certification."))}
	record := map[string]any{"skill": "code-review", "result": "completed", "candidate_digest": candidate, "axes": map[string]any{"Standards": "passed", "Spec": "missing_evidence"}, "reviewer": map[string]any{"actor_id": "synthetic-reviewer", "runtime_id": "runtime.generic", "instance_id": "review-instance"}, "implementer": map[string]any{"actor_id": "synthetic-worker", "runtime_id": "runtime.generic", "instance_id": "worker-instance"}, "constraint_results": f.rows(t, coverage), "findings": []any{}, "verification_results": []any{check}}
	apTestPut(t, f.root, "review.json", record)
	state := map[string]any{"review_input": input, "review_result_ref": "review.json"}
	before := backendTestGit(t, f.project, "status", "--porcelain")
	result, e := backendReview(f.session(t), state, nil)
	if e != nil {
		t.Fatal(e)
	}
	if result["status"] != "audited" || result["execution_allowed"] != false {
		t.Fatal("baseline granted execution")
	}
	if after := backendTestGit(t, f.project, "status", "--porcelain"); after != before {
		t.Fatal("validator wrote project")
	}
	check["executed_at"] = "2099-01-01T00:00:00Z"
	apTestPut(t, f.root, "review.json", record)
	if _, err := backendReview(f.session(t), state, nil); err == nil {
		t.Fatal("baseline accepted machine evidence executed in the future")
	}
	t.Log("intentional-strict-difference: baseline audited result also requires a real non-future machine execution timestamp")
	check["executed_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	check["exit_code"] = 1
	apTestPut(t, f.root, "review.json", record)
	if _, e = backendReview(f.session(t), state, nil); e == nil {
		t.Fatal("failed actual check accepted")
	}
}
func TestBackendTerminalRejectsScopeAuthorityAndFalseCompletion(t *testing.T) {
	root := apTestRoot(t)
	s := apTestSession(t, root)
	apTestPut(t, root, ".yss-backend-delivery.json", map[string]any{"schema_version": 1, "kind": "backend-delivery-terminal", "business_completed": false, "release_authorized": false})
	if e := verifyBackendTerminalSemantic(s, ".yss-backend-delivery.json", nil); e == nil {
		t.Fatal("missing explicit scope accepted")
	}
	if _, _, e := backendOpenDelivery(s, "bundle.zip", nil); e == nil {
		t.Fatal("ZIP without readonly source view accepted")
	}
}

func TestBackendCurrentReviewFixedSource(t *testing.T) {
	legacy := os.Getenv("YSS_LEGACY_ORACLE_ROOT")
	if legacy == "" {
		t.Skip("fixed legacy oracle not selected")
	}
	script := `const fs=await import('node:fs');const path=await import('node:path');
const {terminalReviewFixture}=await import(process.argv[1]);const {validateBackendReview}=await import(process.argv[2]);
const f=terminalReviewFixture();const original=validateBackendReview(f.state,{root:f.root});
// Materialize only exact legacy tool files that the original fixture reads
// implicitly. Recompile coverage with those same input bytes for both engines.
for(const ref of ['.template-spec/agents/yss-skill-registry.yaml','.agents/skills/yss-implementation-contract-compiler/references/compiler-contract.yaml','.agents/skills/yss-technical-design/references/technical-design.schema.json'])
 if(!fs.existsSync(path.join(f.root,ref)))f.write(ref,fs.readFileSync(path.join(process.argv[3],ref),'utf8'));
fs.cpSync(path.join(process.argv[3],'.agents/skills'),path.join(f.root,'.agents/skills'),{recursive:true,force:false});
f.refreshCoverage();f.save();f.write('backend-review-state.json',f.state);
const result=validateBackendReview(f.state,{root:f.root});
const reviewFile=path.join(f.root,f.state.review_result_ref), saved=fs.readFileSync(reviewFile,'utf8');
let futureResult;try{const future=JSON.parse(saved);for(const check of future.verification_results)check.executed_at='2099-01-01T00:00:00Z';fs.writeFileSync(reviewFile,JSON.stringify(future));futureResult=validateBackendReview(f.state,{root:f.root});}finally{fs.writeFileSync(reviewFile,saved);}
console.log(JSON.stringify({root:f.root,original,result,futureResult}));`
	cmd := exec.Command("node", "--input-type=module", "-e", script, governanceOracleURL(filepath.Join(legacy, "scripts/fixtures/backend-standards/terminal-fixture.mjs")), governanceOracleURL(filepath.Join(legacy, "scripts/lib/backend-review.mjs")), legacy)
	b, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("fixed current review fixture: %v %s", e, b)
	}
	var fixture struct {
		Root     string         `json:"root"`
		Original map[string]any `json:"original"`
		Result   map[string]any `json:"result"`
		Future   map[string]any `json:"futureResult"`
	}
	if e = json.Unmarshal(b, &fixture); e != nil {
		t.Fatal(e)
	}
	if fixture.Original["status"] != "passed" || fixture.Result["status"] != "passed" || fixture.Future["status"] != "passed" {
		t.Fatalf("legacy current review failed %s", b)
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("retained synthetic oracle fixture: %s", fixture.Root)
			return
		}
		os.RemoveAll(fixture.Root)
	})
	// New native project identity is an explicit fixture consumer field. Source
	// approvals, contracts, registries and Skill bytes remain the legacy fixture's.
	apTestPut(t, fixture.Root, ".template-spec/process/harness-profile.yaml", map[string]any{"schema_version": 2, "profile_id": domain.Profiles["spec"].ID})
	apTestRoot(t)
	for ref, f := range apFixtureBundle.Files {
		if !strings.HasPrefix(ref, ".template-spec/process/schemas/") {
			continue
		}
		if _, e := os.Lstat(filepath.Join(fixture.Root, ref)); e == nil {
			continue
		}
		raw, e := f.Render(map[string]string{"projectName": "synthetic", "businessDomain": "test-only", "teamSize": "2"})
		if e != nil {
			t.Fatal(e)
		}
		apTestPut(t, fixture.Root, ref, raw)
	}
	// The legacy preflight consumes these exact locked tool authorities through
	// its module ROOT. Make that dependency explicit in the synthetic native
	// project; never import a receiver's tracker or change an approval's basis.
	for _, ref := range []string{approvalSkillsRef, ".agents/skills/yss-implementation-contract-compiler/references/compiler-contract.yaml", ".agents/skills/yss-technical-design/references/technical-design.schema.json"} {
		if _, err := os.Lstat(filepath.Join(fixture.Root, ref)); err == nil {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(legacy, ref))
		if err != nil {
			t.Fatal(err)
		}
		apTestPut(t, fixture.Root, ref, raw)
	}
	s := newSemanticSession(context.Background(), fixture.Root, map[string]string{"tool-root": legacy})
	if e = s.authorities(); e != nil {
		t.Fatal(e)
	}
	state, e := s.doc("backend-review-state.json")
	if e != nil {
		t.Fatal(e)
	}
	input := apMap(state["review_input"])
	c, e := loadNativeSlice(s, apText(input["slice_contract_ref"]))
	if e != nil {
		t.Fatal(e)
	}
	normalized, e := backendSelected(s, c, backendUnit(c, apText(input["work_unit_id"])))
	if e != nil {
		t.Fatal(e)
	}
	project, e := backendProjectRoot(s, input)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.registerExternalRoot(project, c.Ref); e != nil {
		t.Fatal(e)
	}
	current, e := backendCompileCoverage(s, project, normalized, "change", nil, apText(input["review_base_ref"]), nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	recorded, e := s.doc(apText(input["standards_coverage_ref"]))
	if e != nil {
		t.Fatal(e)
	}
	if !apEqual(current, recorded) {
		for key, got := range current {
			if !apEqual(got, recorded[key]) {
				t.Logf("coverage %s differs: native=%s legacy=%s", key, apCanonical(got), apCanonical(recorded[key]))
			}
		}
		t.Fatal("complete current v3 coverage differs from fixed source")
	}
	if e = s.verify("backend-review", "backend-review-state.json", nil); e != nil {
		t.Fatal(e)
	}
	if e = s.finish(); e != nil {
		t.Fatal(e)
	}
	if s.report.Coverage["status"] != "passed" {
		t.Fatal("current review did not pass")
	}
	resultRef := apText(state["review_result_ref"])
	review, e := s.doc(resultRef)
	if e != nil {
		t.Fatal(e)
	}
	for _, check := range apRows(review["verification_results"]) {
		check["executed_at"] = "2099-01-01T00:00:00Z"
	}
	apTestPut(t, fixture.Root, resultRef, review)
	future := newSemanticSession(context.Background(), fixture.Root, map[string]string{"tool-root": legacy})
	if e = future.authorities(); e != nil {
		t.Fatal(e)
	}
	if e = future.verify("backend-review", "backend-review-state.json", nil); e == nil {
		t.Fatal("current change review accepted future machine evidence")
	}
	t.Logf("intentional-strict-difference current-machine-future legacy_accepted=true native_accepted=false legacy_result=%s", apCanonical(fixture.Future))
	t.Logf("fixed-backend-current-review-raw %s", b)
}

func TestBackendCurrentTerminalFixedSource(t *testing.T) {
	legacy := os.Getenv("YSS_LEGACY_ORACLE_ROOT")
	if legacy == "" {
		t.Skip("fixed legacy oracle not selected")
	}
	script := `const fs=await import('node:fs'),path=await import('node:path');const legacy=process.argv[1];
const {pathToFileURL}=await import('node:url');
const load=ref=>import(pathToFileURL(path.join(legacy,ref)).href);
const {terminalReviewFixture}=await load('scripts/fixtures/backend-standards/terminal-fixture.mjs');
const {fixture:strategyFixture}=await load('scripts/fixtures/strategic-handoff/fixture.mjs');
const {attachArtifactApproval}=await load('scripts/fixtures/backend-delivery/approval-fixture.mjs');
const {exportBundle}=await load('scripts/lib/strategic-handoff.mjs');
const {exportBackendDelivery,backendDeliveryBasis}=await load('scripts/lib/backend-delivery.mjs');
const {completeBackendDelivery,verifyBackendDeliveryTerminal}=await load('scripts/lib/backend-delivery-terminal.mjs');
const {hash,read}=await load('scripts/lib/strategic-handoff-io.mjs');
const f=terminalReviewFixture();const source=path.join(f.root,'.template-source/test-source/strategy');fs.mkdirSync(source,{recursive:true});
await strategyFixture(source,{handoffVersion:4});
const strategy=await exportBundle({sourceRoot:source,handoffRef:'handoff.yaml',output:path.join(f.root,'.template-source/test-packages/strategy')});
f.write('.agents/skills/yss-product-lifecycle/references/orchestration-contract.yaml',fs.readFileSync(path.join(legacy,'.agents/skills/yss-product-lifecycle/references/orchestration-contract.yaml'),'utf8'));
f.write('.yss-execution-scope.yaml',{schema_version:1,scope_id:'plan-to-backend'});
const roles=read(path.join(f.root,'.template-spec/agents/digital-human-roles.yaml'));
f.write('.template-spec/agents/digital-human-roles.yaml',roles);
const api=attachArtifactApproval(f.root,'api.yaml','api.synthetic','gate.engineering-contract-approved');
// A same-name captured checkpoint is not selected by the delivery binding.
// Its presence must never let a receiving option supply source expectations.
f.write('receiving-checkpoint.json',{gates:{'gate.engineering-contract-approved':{approval_context:api.binding.approval_context}}});
const file=ref=>({ref,digest:hash(fs.readFileSync(path.join(f.root,ref)))});
f.write('data.md','Synthetic data');f.write('delivery-check.log','Synthetic success/failure evidence. Not real service validation.');
const delivery={schema_version:1,delivery_id:'backend-delivery.synthetic',version:'v1',status:'verified',strategic_bundle_ref:'.template-source/test-packages/strategy',strategic_bundle_digest:strategy.bundle_digest,strategic_route_id:'route.backend',scope:{slice_id:f.contract.slice_id,source_ids:['rule.complete','scenario.submit'],operation_ids:['submitSupplier']},openapi:api.binding,slice_contract:f.binding,build:{source_commit:f.git('rev-parse','HEAD'),artifact_digest:'sha256:'+'b'.repeat(64)},environment:{id:'synthetic-local',base_url:'http://127.0.0.1:1',deployment_id:'synthetic-v1',revision_path:'/version',revision_pointers:{deployment_id:'/deployment_id',source_commit:'/source_commit',openapi_digest:'/openapi_digest',artifact_digest:'/artifact_digest',test_data_digest:'/test_data_digest'},test_data:file('data.md')},verification:{},supporting_files:[]};
for(const [name,kind]of [['contract','backend-contract'],['deployment','backend-deployment']]) {
 f.write(name+'-test.json',{schema_version:1,kind,subject_digest:backendDeliveryBasis(delivery),results:[{command:'synthetic mechanism verification',exit_code:0,executed_at:new Date().toISOString(),evidence:[file('delivery-check.log')]}],operation_ids:['submitSupplier'],coverage:[{source_id:'scenario.submit',outcome:'success'},{source_id:'scenario.submit',outcome:'failure'}]});
 delivery.verification[name]=file(name+'-test.json');
}
function all(dir,prefix=''){return fs.readdirSync(dir,{withFileTypes:true}).flatMap(e=>{const ref=prefix?prefix+'/'+e.name:e.name;if(['project','.template-source/test-source/strategy','.template-source/test-packages/strategy'].includes(ref)||e.name==='.git')return[];return e.isDirectory()?all(path.join(dir,e.name),ref):[ref];});}
delivery.supporting_files=[...all(f.root).filter(ref=>!['coverage.json','review-result.json','checks.log','skills-lock.json'].includes(ref)),'project/mvnw'];
f.write('delivery.json',delivery);f.write('review-state.json',f.state);
const exported=await exportBackendDelivery({sourceRoot:f.root,deliveryRef:'delivery.json',output:path.join(f.root,'.template-source/test-packages/backend')});
const input={delivery:file('delivery.json'),review_state:file('review-state.json'),bundle_ref:'.template-source/test-packages/backend',bundle_digest:exported.bundle_digest,downstream:{owner:'synthetic-receiver',ticket_ref:'tickets/frontend.md',verification_plan:'receiver tests',target_version:'v1'}};
// Exact legacy terminal test dependencies; both engines see the same coverage
// inputs. No receiver defaults are substituted into the source package.
for(const ref of ['scripts','.template-spec/process/schemas','.template-spec/process/lifecycle-registry-baseline.json','.template-spec/process/lifecycle-registry-baseline-v1.json','.template-spec/agents/yss-skill-registry.yaml','.agents/skills','skills-lock.json']){
 fs.mkdirSync(path.dirname(path.join(f.root,ref)),{recursive:true});fs.cpSync(path.join(legacy,ref),path.join(f.root,ref),{recursive:true});
}
f.refreshCoverage();f.save();f.write('review-state.json',f.state);input.review_state=file('review-state.json');
const result=await completeBackendDelivery(f.root,input);const repeated=await verifyBackendDeliveryTerminal(f.root);
console.log(JSON.stringify({root:f.root,result,repeated}));`
	cmd := exec.Command("node", "--input-type=module", "-e", script, legacy)
	b, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("fixed current terminal fixture: %v %s", e, b)
	}
	var fixture struct {
		Root     string         `json:"root"`
		Result   map[string]any `json:"result"`
		Repeated map[string]any `json:"repeated"`
	}
	if e = json.Unmarshal(b, &fixture); e != nil {
		t.Fatal(e)
	}
	if fixture.Result["result"] != "backend-delivered" || !apEqual(fixture.Result, fixture.Repeated) {
		t.Fatalf("legacy terminal did not pass or resume unchanged: %s", b)
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("retained synthetic oracle fixture: %s", fixture.Root)
			return
		}
		os.RemoveAll(fixture.Root)
	})
	apTestPut(t, fixture.Root, ".template-spec/process/harness-profile.yaml", map[string]any{"schema_version": 2, "profile_id": domain.Profiles["spec"].ID})
	s := newSemanticSession(context.Background(), fixture.Root, map[string]string{"tool-root": legacy})
	if e = s.authorities(); e != nil {
		t.Fatal(e)
	}
	if _, e = backendInspectDelivery(s, "delivery.json", nil); e != nil {
		t.Fatalf("live delivery source: %v", e)
	}
	if e = s.verify("backend-terminal", ".yss-backend-delivery.json", nil); e != nil {
		t.Fatal(e)
	}
	if e = s.finish(); e != nil {
		t.Fatal(e)
	}
	if !apEqual(s.report.Coverage, fixture.Result) {
		t.Fatalf("terminal output differs: native=%v legacy=%v", s.report.Coverage, fixture.Result)
	}
	// The same producer's entire directory package must pass as immutable ZIP
	// bytes, including its nested strategic package and captured source policy.
	backendTestDirectoryZIP(t, fixture.Root, ".template-source/test-packages/backend", "backend-package.zip")
	zipSession := newSemanticSession(context.Background(), fixture.Root, map[string]string{"tool-root": legacy})
	if e = zipSession.authorities(); e != nil {
		t.Fatal(e)
	}
	manifest, _, e := backendOpenDelivery(zipSession, "backend-package.zip", map[string]string{"checkpoint": "receiving-checkpoint.json", "profile": "frontend", "task": "receiving-task.json"})
	if e != nil {
		t.Fatalf("immutable ZIP delivery: %v", e)
	}
	terminal, e := zipSession.doc(".yss-backend-delivery.json")
	if e != nil || manifest["bundle_digest"] != terminal["bundle_digest"] {
		t.Fatalf("ZIP did not consume the same complete producer package: %v", e)
	}
	if e = zipSession.finish(); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(fixture.Root, ".yss-archive-view")); !os.IsNotExist(e) {
		t.Fatal("ZIP verification extracted or created a physical archive view")
	}
	apTestPut(t, fixture.Root, "backend-package.zip", []byte("changed archive"))
	if e = zipSession.finish(); e == nil {
		t.Fatal("archive bytes changed after verification without invalidating the result")
	}
	// Demonstrate the exact receiving-selector hazard at the source approval
	// seam, then exercise the complete manifest-validated package after removal
	// of its independently declared consumer context.
	packageRef := ".template-source/test-packages/backend"
	sourceSession := newSemanticSession(context.Background(), fixture.Root, map[string]string{"tool-root": legacy})
	if e = sourceSession.authorities(); e != nil {
		t.Fatal(e)
	}
	packageManifest, source, e := backendOpenDelivery(sourceSession, packageRef, nil)
	if e != nil {
		t.Fatal(e)
	}
	delivery, e := source.doc(apText(packageManifest["delivery_ref"]))
	if e != nil {
		t.Fatal(e)
	}
	delivery = apTestJSONClone(t, delivery)
	missingContext := apMap(delivery["openapi"])
	delete(missingContext, "approval_context")
	approval, e := source.doc(apText(missingContext["approval_ref"]))
	if e != nil {
		t.Fatal(e)
	}
	receivingOpts := map[string]string{"checkpoint": "receiving-checkpoint.json", "profile": "frontend", "task": "receiving-task.json"}
	if e = backendArtifactApproval(source, apText(approval["gate_id"]), missingContext, approval, receivingOpts); e != nil {
		t.Fatalf("unsafe receiving selector did not reproduce the source approval hazard: %v", e)
	}
	if e = backendArtifactApproval(source, apText(approval["gate_id"]), missingContext, approval, backendSourceOptions(receivingOpts)); e == nil {
		t.Fatal("source approval accepted the receiving checkpoint without its own independent context")
	}
	body := map[string]any{}
	for _, key := range []string{"delivery_id", "version", "strategic_bundle_digest", "strategic_route_id", "scope", "openapi", "slice_contract", "build", "environment"} {
		if value, present := delivery[key]; present {
			body[key] = value
		}
	}
	for _, kind := range []string{"contract", "deployment"} {
		binding := apMap(apMap(delivery["verification"])[kind])
		proof, e := source.doc(apText(binding["ref"]))
		if e != nil {
			t.Fatal(e)
		}
		proof = apTestJSONClone(t, proof)
		proof["subject_digest"] = apDigest(body)
		raw := backendTestPackageReplace(t, fixture.Root, packageRef, packageManifest, apText(binding["ref"]), proof)
		binding["digest"] = backendHash(raw)
	}
	backendTestPackageReplace(t, fixture.Root, packageRef, packageManifest, "delivery.json", delivery)
	packageManifest["bundle_digest"] = apDigest(contractWithout(packageManifest, "bundle_digest"))
	apTestPut(t, fixture.Root, packageRef+"/manifest.json", packageManifest)
	for _, input := range []string{packageRef, "backend-receiving-checkpoint.zip"} {
		if strings.HasSuffix(input, ".zip") {
			backendTestDirectoryZIP(t, fixture.Root, packageRef, input)
		}
		testSession := newSemanticSession(context.Background(), fixture.Root, map[string]string{"tool-root": legacy, "checkpoint": "receiving-checkpoint.json", "profile": "spec"})
		if e = testSession.authorities(); e != nil {
			t.Fatal(e)
		}
		if _, _, e = backendOpenDelivery(testSession, input, receivingOpts); e == nil {
			t.Fatalf("%s borrowed receiving checkpoint to repair absent source approval context", input)
		}
		t.Logf("source-receiving-selector refused package=%s error=%v", input, e)
	}
	// A real source mutation must block current resume even though the saved
	// terminal, package receipt and genuine original approvals still exist.
	apTestPut(t, fixture.Root, "checks.log", "stale independent review evidence")
	s = newSemanticSession(context.Background(), fixture.Root, map[string]string{"tool-root": legacy})
	if e = s.authorities(); e != nil {
		t.Fatal(e)
	}
	if e = s.verify("backend-terminal", ".yss-backend-delivery.json", nil); e == nil {
		t.Fatal("terminal accepted stale independent review bytes")
	}
}

func backendTestPackageReplace(t *testing.T, root, prefix string, manifest map[string]any, original string, value map[string]any) []byte {
	t.Helper()
	for _, row := range apRows(manifest["files"]) {
		if row["original_ref"] == original {
			raw := apTestPut(t, root, prefix+"/"+apText(row["path"]), value)
			row["sha256"] = backendHash(raw)
			row["size_bytes"] = len(raw)
			return raw
		}
	}
	t.Fatalf("producer manifest did not capture source ref %s", original)
	return nil
}

func TestBackendPackageZIPRejectsUnsafeAndIncompleteMembers(t *testing.T) {
	for _, names := range [][]string{{"../escape"}, {"Straße.txt", "STRASSE.txt"}, {"A/x", "a/y"}, {"same", "same"}, {"unregistered.txt"}} {
		t.Run(names[0], func(t *testing.T) {
			root := apTestRoot(t)
			apTestPut(t, root, "backend.zip", zipTestBytes(t, names))
			s := apTestSession(t, root)
			if _, _, e := backendOpenDelivery(s, "backend.zip", nil); e == nil {
				t.Fatal("invalid ZIP was accepted as a complete backend source")
			}
			if _, e := os.Stat(filepath.Join(root, ".yss-archive-view")); !os.IsNotExist(e) {
				t.Fatal("failed archive verification created an extraction directory")
			}
		})
	}
}

func TestBackendWorktreePackedCurrentModeBytesAndInventory(t *testing.T) {
	for _, mutate := range []string{"none", "tracked", "untracked", "mode", "inventory"} {
		t.Run(mutate, func(t *testing.T) {
			f := backendTestFixture(t)
			apTestPut(t, f.root, "project/src/main/java/app/Service.java", "package app; public class Service { String login(){return \"updated\";} }")
			raw := []byte("synthetic untracked asset")
			apTestPut(t, f.root, "project/new.txt", raw)
			s := f.session(t)
			diff, e := s.git(f.project, "diff", "--binary", "--full-index", f.head)
			if e != nil {
				t.Fatal(e)
			}
			var stream bytes.Buffer
			stream.WriteString("YSS-WORKTREE-CANDIDATE-V1\x00")
			stream.WriteByte(0x54)
			binary.Write(&stream, binary.BigEndian, uint64(len(diff)))
			stream.Write(diff)
			stream.WriteByte(0x55)
			binary.Write(&stream, binary.BigEndian, uint64(len("new.txt")))
			stream.WriteString("new.txt")
			binary.Write(&stream, binary.BigEndian, uint32(0100644))
			stream.WriteByte(0x52)
			binary.Write(&stream, binary.BigEndian, uint64(len(raw)))
			stream.Write(raw)
			base := ".template-source/evidence/maintenance/snapshot"
			ref := base + "/candidate-manifest.yaml"
			manifest := map[string]any{"schema_version": 1, "candidate_kind": "yss-worktree-candidate-v1", "storage": "packed-stream", "review_mode": "worktree", "review_base_ref": f.head, "merge_base": f.head, "implementation_candidate_ref": "working-tree", "candidate_snapshot_ref": ref, "candidate_digest": safefs.Digest(stream.Bytes()), "tracked_diff_command": "git diff synthetic", "commit_list_command": "git log synthetic", "untracked_inventory_command": "git ls-files synthetic", "untracked_diff_command": "packed in candidate.bin", "untracked_files": []any{"new.txt"}, "untracked_path_bytes": []any{base64.StdEncoding.EncodeToString([]byte("new.txt"))}, "excluded_paths": []any{base}, "snapshot_stream_ref": base + "/candidate.bin", "tracked_diff_ref": base + "/tracked.diff"}
			apTestPut(t, f.project, ref, manifest)
			apTestPut(t, f.project, base+"/candidate.bin", stream.Bytes())
			apTestPut(t, f.project, base+"/tracked.diff", diff)
			switch mutate {
			case "tracked":
				apTestPut(t, f.root, "project/src/main/java/app/Service.java", "package app; class Service { String manual; }")
			case "untracked":
				apTestPut(t, f.root, "project/new.txt", "manual changed")
			case "mode":
				if e = os.Chmod(filepath.Join(f.project, "new.txt"), 0755); e != nil {
					t.Fatal(e)
				}
			case "inventory":
				apTestPut(t, f.root, "project/other.txt", "manual new file")
			}
			_, e = backendWorktreeCurrent(f.session(t), f.project, map[string]any{"candidate_snapshot_ref": ref, "candidate_digest": manifest["candidate_digest"]})
			if mutate == "none" && e != nil {
				t.Fatal(e)
			}
			if mutate != "none" && e == nil {
				t.Fatal("worktree drift accepted")
			}
		})
	}
}

func TestBackendPackageRejectsUnregisteredAndCaseCollision(t *testing.T) {
	for _, which := range []string{"case", "extra", "bytes", "escape"} {
		t.Run(which, func(t *testing.T) {
			root := apTestRoot(t)
			s := newSemanticSession(context.Background(), root, nil)
			b := apTestPut(t, root, "package/payload/a.txt", "Synthetic payload")
			files := []any{map[string]any{"path": "payload/a.txt", "size_bytes": len(b), "sha256": backendHash(b), "original_ref": "a.txt"}}
			switch which {
			case "case":
				apTestPut(t, root, "package/payload/A.txt", b)
				files = append(files, map[string]any{"path": "payload/A.txt", "size_bytes": len(b), "sha256": backendHash(b), "original_ref": "b.txt"})
			case "extra":
				apTestPut(t, root, "package/unregistered.txt", "unknown")
			case "bytes":
				apMap(files[0])["sha256"] = backendHash([]byte("different"))
			case "escape":
				apMap(files[0])["path"] = "../escape"
			}
			manifest := map[string]any{"schema_version": 1, "kind": "backend-delivery", "delivery_id": "backend-delivery.synthetic", "version": "v1", "delivery_ref": "delivery.json", "files": files}
			manifest["bundle_digest"] = apDigest(manifest)
			apTestPut(t, root, "package/manifest.json", manifest)
			_, _, e := backendOpenDelivery(s, "package", nil)
			apTestCode(t, e, "BACKEND_DELIVERY")
		})
	}
}
