package helpview

import (
	"encoding/base64"
	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"strings"
	"testing"
)

func TestSpecViewDoesNotRequireProductStages(t *testing.T) {
	raw := []byte(`{"schemaVersion":1,"profile":"spec","specWorkflow":"matt-spec-v1"}`)
	b := &bundle.Bundle{Profile: "spec", TemplateCommit: strings.Repeat("a", 40), Files: map[string]bundle.File{".template-spec/process/spec-workflow.json": {Data: base64.StdEncoding.EncodeToString(raw), Digest: safefs.Digest(raw)}}, Installations: map[string]bundle.Installation{"governed": {Distribution: map[string]any{"specWorkflow": "matt-spec-v1"}}}}
	v, handled, err := deriveSpecWorkflow(b)
	if err != nil || !handled || len(v.Stages) != 0 || v.EntryWorkUnit != "" || v.DailyEnabled || len(v.WorkflowSteps) == 0 {
		t.Fatalf("new Spec still requires stages: %+v %v %v", v, handled, err)
	}
	delete(b.Files, ".template-spec/process/spec-workflow.json")
	if _, _, err := deriveSpecWorkflow(b); err == nil {
		t.Fatal("missing source policy accepted")
	}
}
