package project

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func TestWorkLayoutMutableReferencesKeepCommentsAnchorsAndBindSha256(t *testing.T) {
	raw := []byte("# human note\nbasis:\n  ref: 'docs/.scratch/report/spec.md#acceptance' # stable comment\n  sha256: sha256:" + strings.Repeat("0", 64) + "\nhistory:\n  ref: docs/.scratch/report/old.md\n")
	doc, e := parseWorkDocument(raw, ".yaml")
	if e != nil {
		t.Fatal(e)
	}
	doc.value = workReferences(doc.value, map[string]string{"docs/.scratch/report/spec.md": ".work/report/spec.md"}, "")
	workBindings(doc.value, map[string][]byte{".work/report/spec.md": []byte("new")}, "")
	after, e := doc.encode()
	if e != nil {
		t.Fatal(e)
	}
	for _, want := range []string{"# human note", "# stable comment", "'.work/report/spec.md#acceptance'", "sha256:" + safefs.Digest([]byte("new")), "ref: docs/.scratch/report/old.md"} {
		if !strings.Contains(string(after), want) {
			t.Fatalf("lost %s: %s", want, after)
		}
	}
}

func TestWorkLayoutRelativeSchemaReferencesMustClose(t *testing.T) {
	inputs := map[string]domain.Descriptor{".work/report/api/types.yaml": {Type: "file"}}
	for _, test := range []struct {
		ref   string
		valid bool
	}{{"./types.yaml#/Thing", true}, {"#/components/Thing", true}, {"./missing.yaml", false}, {"../../../../outside.yaml", false}, {"https://example.invalid/types.yaml", false}} {
		blocked := false
		workCheckReferences(map[string]any{"$ref": test.ref}, nil, inputs, nil, ".work/report/api/api.yaml", "docs/.scratch", "", func(code, reason string) { blocked = true })
		if blocked == test.valid {
			t.Fatalf("relative reference %s valid=%v blocked=%v", test.ref, test.valid, blocked)
		}
	}
}

func TestWorkLayoutRebindingRejectsChangedApprovalWithoutNewEvidence(t *testing.T) {
	old := []byte(`{"gate_id":"gate.plan-approved","decision":"approved","subject_ref":"docs/.scratch/report/spec.md","user_decision_ref":"old-decision.json"}`)
	reused := []byte(`{"gate_id":"gate.plan-approved","decision":"approved","subject_ref":".work/report/spec.md","user_decision_ref":"old-decision.json"}`)
	if e := workRebindingBytes("docs/.scratch/report/gates/plan.json", old, reused); e == nil {
		t.Fatal("path-only approval renewal accepted")
	}
	if e := workRebindingBytes("docs/.scratch/report/user-decisions/message.md", []byte("human original"), []byte("changed")); e == nil {
		t.Fatal("raw human original overwritten")
	}
}

func TestWorkLayoutApprovalResolutionCannotBypassCurrentEvidence(t *testing.T) {
	root := workNativeFixture(t)
	ref := "docs/.scratch/report/gates/approval.json"
	workFile(t, root, "docs/.scratch/report/spec.md", "# draft")
	workFile(t, root, ref, `{"gate_id":"gate.plan-approved","decision":"approved","subject_ref":"docs/.scratch/report/spec.md"}`)
	original, e := BuildWorkLayout(root, "")
	if e != nil {
		t.Fatal(e)
	}
	var asset AssetResult
	for _, a := range original.Assets {
		if a.Path == ref {
			asset = a
		}
	}
	after := []byte(`{"gate_id":"gate.plan-approved","decision":"approved","subject_ref":".work/report/spec.md","user_decision_ref":".work/report/missing-decision.json"}`)
	resolution := Resolution{Path: ref, Choice: "use-merged", Before: asset.Before, Target: asset.Target, RuleID: asset.RuleID, TargetPath: asset.TargetPath, CandidateDigest: safefs.Digest(after), CandidateData: base64.StdEncoding.EncodeToString(after)}
	candidate, e := BuildWorkLayoutWithOptions(root, "", PlanningOptions{ResolvedFrom: original.Digest, Resolutions: []Resolution{resolution}})
	if e != nil {
		t.Fatal(e)
	}
	if candidate.ReadyToApply {
		t.Fatal("fabricated fresh approval accepted")
	}
	if _, e := Apply(candidate); e == nil {
		t.Fatal("blocked rebinding applied")
	}
}
