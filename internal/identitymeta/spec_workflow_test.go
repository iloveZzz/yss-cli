package identitymeta

import "testing"

func TestSpecWorkflowDeclaration(t *testing.T) {
	for _, tc := range []struct {
		name, profile string
		installation  string
		schema        int
		value         any
		present, bad  bool
		want          string
	}{
		{name: "legacy", profile: "spec"},
		{name: "new", profile: "spec", value: "matt-spec-v1", present: true, want: "matt-spec-v1"},
		{name: "unknown", profile: "spec", value: "matt-spec-v2", present: true, bad: true},
		{name: "empty", profile: "spec", value: "", present: true, bad: true},
		{name: "type", profile: "spec", value: 7, present: true, bad: true},
		{name: "backend", profile: "backend"},
		{name: "skills-installation", profile: "spec", value: "matt-spec-v1", present: true, bad: true, installation: "skills"},
		{name: "old-schema", profile: "spec", value: "matt-spec-v1", present: true, bad: true, schema: 4},
		{name: "wrong-profile", profile: "backend", value: "matt-spec-v1", present: true, bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &Metadata{SchemaVersion: 5, Profile: tc.profile, Distribution: map[string]any{"installation": "governed"}}
			if tc.installation != "" {
				m.Distribution["installation"] = tc.installation
			}
			if tc.schema != 0 {
				m.SchemaVersion = tc.schema
			}
			if tc.present {
				m.Distribution["specWorkflow"] = tc.value
			}
			got, err := SpecWorkflow(m)
			if (err != nil) != tc.bad || got != tc.want {
				t.Fatalf("got %q, %v; want %q bad=%v", got, err, tc.want, tc.bad)
			}
		})
	}
}
