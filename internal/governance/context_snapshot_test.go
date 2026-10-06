package governance_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/governance"
)

func TestContextCheckReturnsConsumableSnapshot(t *testing.T) {
	root := canonicalTemp(t)
	put(t, root, "CONTEXT.md", validContext)
	for _, refs := range []string{"", "Reporting/Report", "Reporting/Report,Reporting/Report"} {
		result, err := governance.Run("context", "check", root, map[string]string{"term-refs": refs})
		if err != nil {
			t.Fatal(err)
		}
		bytes, _ := json.Marshal(result)
		var output map[string]any
		if err := json.Unmarshal(bytes, &output); err != nil {
			t.Fatal(err)
		}
		snapshot, ok := output["context_snapshot"].(map[string]any)
		if !ok {
			t.Fatalf("missing consumable snapshot: %s", bytes)
		}
		if snapshot["context_ref"] != "CONTEXT.md" || snapshot["document_digest"] != output["document_digest"] {
			t.Fatal(snapshot)
		}
		count := 0
		if refs != "" {
			count = len(strings.Split(refs, ","))
		}
		if len(snapshot["term_refs"].([]any)) != count {
			t.Fatal("snapshot must preserve requested references", snapshot)
		}
		if refs == "" && snapshot["referenced_terms_digest"] != "sha256:4f53cda18c2baa0c0354bb5f9a3ecbe5ed12ab4d8e11ba873c2f11161202b945" {
			t.Fatal("wrong empty-set digest", snapshot)
		}
		put(t, root, "snapshot.json", string(mustJSON(t, snapshot)))
		if _, err := governance.Run("context", "verify", root, map[string]string{"snapshot": "snapshot.json"}); err != nil {
			t.Fatal("snapshot cannot be consumed", err)
		}
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
