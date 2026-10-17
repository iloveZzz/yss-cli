package bundle

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestSelectedSourceLockProjectsClaudeRuntime(t *testing.T) {
	t.Parallel()
	lock := []byte(`{"version":3,"projectionRoots":[],"skills":{"shared":{"a":{"targets":[".agents/skills"]},"b":{"targets":[".agents/skills"]}},"platform":{}}}`)
	out, err := SelectedSourceLock(lock, []string{"a"}, []string{"claude"})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		ProjectionRoots []string `json:"projectionRoots"`
		Skills          struct {
			Shared map[string]struct {
				Targets []string `json:"targets"`
			} `json:"shared"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.ProjectionRoots, []string{".claude/skills"}) {
		t.Errorf("projectionRoots = %v", got.ProjectionRoots)
	}
	if _, kept := got.Skills.Shared["b"]; kept || len(got.Skills.Shared) != 1 {
		t.Errorf("只应保留已选技能: %v", got.Skills.Shared)
	}
	if want := []string{".agents/skills", ".claude/skills"}; !reflect.DeepEqual(got.Skills.Shared["a"].Targets, want) {
		t.Errorf("targets = %v, want %v", got.Skills.Shared["a"].Targets, want)
	}

	none, err := SelectedSourceLock(lock, []string{"a"}, nil)
	if err != nil || !json.Valid(none) {
		t.Fatalf("无运行时也应生成锁: %v", err)
	}
	for _, bad := range [][]string{{"vscode"}, {"claude", "claude"}} {
		if _, err := SelectedSourceLock(lock, []string{"a"}, bad); err == nil {
			t.Errorf("运行时 %v 应被拒绝", bad)
		}
	}
}
