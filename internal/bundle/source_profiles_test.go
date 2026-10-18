package bundle

import "testing"

func TestSourceProfilesRejectsMixedGenerations(t *testing.T) {
	for _, tc := range []struct {
		schema int
		names  []string
		valid  bool
	}{
		{3, []string{"spec", "design", "backend"}, true},
		{5, []string{"spec", "backend"}, true},
		{3, []string{"spec", "backend"}, false},
		{5, []string{"spec", "design", "backend"}, false},
		{5, []string{"spec", "other"}, false},
		{6, []string{"spec", "backend"}, false},
	} {
		lock := SourceLock{SchemaVersion: tc.schema, Profiles: map[string]Source{}}
		for _, name := range tc.names {
			lock.Profiles[name] = Source{Profile: name}
		}
		_, err := SourceProfiles(lock)
		if (err == nil) != tc.valid {
			t.Errorf("schema %d profiles %v: %v", tc.schema, tc.names, err)
		}
	}
}
