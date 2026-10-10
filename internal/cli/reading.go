package cli

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

func renderContractReading(result any) string {
	m := result.(map[string]any)
	var out strings.Builder
	fmt.Fprintf(&out, "# Slice 阅读视图 · %v\n\n只读；批准有效性未检查，不授予执行权限。\n", m["view"])
	for _, key := range []string{"binding", "project_identity", "checks", "blockers"} {
		b, _ := json.MarshalIndent(m[key], "", "  ")
		fmt.Fprintf(&out, "\n## %s\n\n```json\n%s\n```\n", key, b)
	}
	content := m["content"].(map[string]any)
	keys := []string{}
	for key := range content {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		b, _ := json.MarshalIndent(content[key], "", "  ")
		fmt.Fprintf(&out, "\n## %s\n\n```json\n%s\n```\n", key, b)
	}
	return out.String()
}
