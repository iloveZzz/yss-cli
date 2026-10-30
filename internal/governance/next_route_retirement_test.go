package governance

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
)

func TestRetiredStrategicRouteRejected(t *testing.T) {
	for _, route := range [][2]string{
		{"work-unit.business-ticket-formalization", "work-unit.strategic-design-handoff"},
		{"work-unit.strategic-design-handoff", "work-unit.technical-analysis"},
	} {
		for _, stages := range []bool{false, true} {
			for _, task := range []bool{false, true} {
				root := t.TempDir()
				data := fmt.Sprintf(`{"stage_trace":{"completed_work_unit":%q},"next_work_unit":%q}`, route[0], route[1])
				if err := os.WriteFile(filepath.Join(root, "checkpoint.json"), []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "task.json"), []byte(`{}`), 0600); err != nil {
					t.Fatal(err)
				}
				s := kernel.NewSemanticSession(context.Background(), root, nil)
				if stages {
					s.GovernanceVersion = 2
					s.Report.Profile = "spec"
					s.GovernancePolicy = map[string]any{"stage_gates": map[string]any{"capability": kernel.StageGatesCapability}}
				}
				ref, opts := "checkpoint.json", map[string]string(nil)
				if task {
					ref = "task.json"
					opts = map[string]string{"current": route[0], "next": route[1], "checkpoint": "checkpoint.json"}
				}
				if got := kernel.SemanticCode(verifyNextRouteSemantic(s, ref, opts)); got != "UNPORTED" {
					t.Fatalf("route=%v stage=%v task=%v code=%q, want UNPORTED", route, stages, task, got)
				}
			}
		}
	}
}
