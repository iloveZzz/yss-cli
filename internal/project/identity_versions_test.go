package project

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestDetectRoleIndependentIdentityTuple(t *testing.T) {
	for _, tc := range []struct {
		root, profile string
		want          bool
	}{{"1", "2", true}, {"2", "3", true}, {"2", "2", false}, {"1", "3", false}, {"2.5", "3", false}, {"2", "99", false}} {
		t.Run(tc.root+"-"+tc.profile, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err = os.MkdirAll(filepath.Join(root, ".template-spec/process"), 0755); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(root, "yss-project.yaml"), []byte(fmt.Sprintf("schema_version: %s\nrepository_mode: project-instance\n", tc.root)), 0644); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(root, ".template-spec/process/harness-profile.yaml"), []byte(fmt.Sprintf("schema_version: %s\nprofile_id: harness.spec-template\n", tc.profile)), 0644); err != nil {
				t.Fatal(err)
			}
			_, err = Detect(root, "spec", false)
			if (err == nil) != tc.want {
				t.Fatalf("identity tuple %s/%s: %v", tc.root, tc.profile, err)
			}
		})
	}
}
