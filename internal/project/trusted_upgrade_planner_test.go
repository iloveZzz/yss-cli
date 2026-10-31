//go:build !yss_nobundle

package project

import (
	"encoding/base64"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func TestSyncPlansWorkflowChangeFromTrustedBaseline(t *testing.T) {
	baseline, err := bundle.Load("spec")
	if err != nil {
		t.Fatal(err)
	}
	root, _ := writeInstallationBundleFixtureWithKind(t, baseline, InstallationGoverned)
	if _, err := Detect(root, "spec", false); err != nil {
		t.Fatalf("official 5.0 baseline was not trusted: %v", err)
	}

	// A synthetic target isolates planner behavior; it is not release or apply evidence.
	target, err := bundle.Load("spec")
	if err != nil {
		t.Fatal(err)
	}
	file := target.Files[SpecWorkflowRef]
	raw, err := file.Render(nil)
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	file.Data = base64.StdEncoding.EncodeToString(raw)
	file.Digest = safefs.Digest(raw)
	target.Files[SpecWorkflowRef] = file
	governed := target.Installations["governed"]
	governed.Initial[SpecWorkflowRef] = file
	target.Installations["governed"] = governed
	syncTestSnapshot(t, root)
	plan, err := buildCore(root, "spec", "sync", nil, nil, nil, PlanningOptions{target: target})
	if err != nil {
		t.Fatalf("planner rejected trusted installed source before comparing target: %v", err)
	}
	for _, change := range plan.Changes {
		if change.Path == SpecWorkflowRef {
			return
		}
	}
	t.Fatal("planner did not report changed workflow policy")
}
