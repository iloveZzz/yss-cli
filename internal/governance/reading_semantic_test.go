package governance

import (
	"context"
	"os"
	"testing"
)

func TestReadingFixedSourceDifferential(t *testing.T) {
	root := os.Getenv("YSS_READING_ORACLE_FIXTURE")
	if root == "" {
		t.Skip("fixed source managed reading fixture not configured")
	}
	s := newSemanticSession(context.Background(), root, map[string]string{"tool-root": os.Getenv("YSS_LEGACY_ORACLE_ROOT")})
	var err error
	s.registry, err = s.doc(approvalRegistryRef)
	if err != nil {
		t.Fatal(err)
	}
	s.roles, err = s.doc(approvalRolesRef)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyReadingTransitionSemantic(s, "docs/.scratch/demo/checkpoint.yaml", nil); err != nil {
		if expected, e := s.buildReading("docs/.scratch/demo/checkpoint.yaml"); e == nil {
			manifest, _ := s.doc("docs/.scratch/demo/reading/.manifest.json")
			actual := map[string]any{}
			for _, v := range semList(manifest["dependencies"]) {
				row := semMap(v)
				actual[text(row["scope"])+" "+text(row["ref"])] = row["digest"]
			}
			for ref, value := range expected.ProjectRefs {
				key := "project " + ref
				if !apEqual(actual[key], value) {
					t.Logf("project dependency mismatch %s actual=%v expected=%v", key, actual[key], value)
				}
				delete(actual, key)
			}
			for ref, value := range expected.ToolRefs {
				key := "tool " + ref
				if !apEqual(actual[key], value) {
					t.Logf("tool dependency mismatch %s actual=%v expected=%v", key, actual[key], value)
				}
				delete(actual, key)
			}
			t.Logf("remaining actual dependencies: %+v", actual)
		}
		t.Fatal(err)
	}
	if err := s.finish(); err != nil {
		t.Fatal(err)
	}
}
