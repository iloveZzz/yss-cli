package contracts

import (
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"os"
	"path/filepath"
	"testing"
)

func TestBusinessReview2UsesCurrentPreparedOutputWithoutRoles(t *testing.T) {
	for _, scenario := range []string{"self-check", "legacy-record", "missing-source", "fake-independent", "stale-output"} {
		t.Run(scenario, func(t *testing.T) {
			s, task := completedDraft(t)
			output, _ := os.ReadFile(filepath.Join(s.Root, "drafts/spec.md"))
			rawTask, _ := os.ReadFile(filepath.Join(s.Root, "task.json"))
			log, err := s.Doc("origin.json")
			if err != nil {
				t.Fatal(err)
			}
			log["candidate_digest"] = safefs.Digest(output)
			log["readonly"] = true
			taskFile(t, s.Root, "business-check.json", log)
			checkBytes, _ := os.ReadFile(filepath.Join(s.Root, "business-check.json"))
			provenance := ContractCopy(kernel.ApMap(task["provenance"]))
			provenance["execution_ref"] = "business-check.json"
			review := map[string]any{"schema_version": 2, "kind": "business-ticket-review", "result": "passed", "subject_ref": "drafts/spec.md", "subject_digest": "sha256:" + safefs.Digest(output), "basis": []any{map[string]any{"ref": "task.json", "digest": safefs.Digest(rawTask)}}, "assessment": map[string]any{"mode": "self-check", "provenance": provenance}, "evidence": []any{map[string]any{"ref": "business-check.json", "version": "v1", "digest": "sha256:" + safefs.Digest(checkBytes)}}}
			switch scenario {
			case "legacy-record":
				review["schema_version"] = 1
			case "missing-source":
				review["basis"] = []any{}
			case "fake-independent":
				kernel.ApMap(review["assessment"])["mode"] = "independent-review"
			case "stale-output":
				taskFile(t, s.Root, "drafts/spec.md", map[string]any{"changed": true})
			}
			s.V = kernel.NewView(s.Root)
			err = contractBusinessReview2(s, review, "drafts/spec.md", "sha256:"+safefs.Digest(output))
			if (err == nil) != (scenario == "self-check") {
				t.Fatalf("%s: %v", scenario, err)
			}
		})
	}
}
