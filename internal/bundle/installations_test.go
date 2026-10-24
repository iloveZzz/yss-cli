package bundle

import "testing"

func TestBundleInstallationViews(t *testing.T) {
	b := &Bundle{SchemaVersion: 5, Files: map[string]File{"extra": {}}, Installations: map[string]Installation{
		"skills":   {Initial: map[string]File{"AGENTS.md": encodedFile([]byte("local engineering"), 0644, "managed")}, Distribution: map[string]any{"installation": "skills"}},
		"governed": {Initial: map[string]File{"yss-project.yaml": encodedFile([]byte("identity"), 0644, "managed")}, Distribution: map[string]any{"installation": "governed"}},
	}}
	view, err := b.ForInstallation("skills")
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Initial) != 1 || view.Distribution["installation"] != "skills" {
		t.Fatal("wrong selection")
	}
	delete(view.Initial, "AGENTS.md")
	view.Distribution["installation"] = "changed"
	if len(b.Installations["skills"].Initial) != 1 || b.Installations["skills"].Distribution["installation"] != "skills" {
		t.Fatal("view mutated bundle")
	}
	if _, err = b.ForInstallation("unknown"); err == nil {
		t.Fatal("accepted unknown installation")
	}
}
func TestInstallationRejectsBadReferences(t *testing.T) {
	for _, ref := range []string{"../escape", "/absolute"} {
		b := &Bundle{SchemaVersion: 5, Installations: map[string]Installation{"skills": {Initial: map[string]File{ref: encodedFile([]byte("x"), 0644, "managed")}}}}
		if _, err := b.ForInstallation("skills"); err == nil {
			t.Fatalf("accepted %s", ref)
		}
	}
}
func TestOldBundleOnlySupportsGoverned(t *testing.T) {
	b := &Bundle{SchemaVersion: 4, Initial: map[string]File{"x": {}}, Distribution: map[string]any{}}
	if _, err := b.ForInstallation("skills"); err == nil {
		t.Fatal("old bundle guessed skills")
	}
	if _, err := b.ForInstallation("governed"); err != nil {
		t.Fatal(err)
	}
}

func TestInstallationArchiveBindsSelection(t *testing.T) {
	b := &Bundle{Installations: map[string]Installation{"skills": {Distribution: map[string]any{"installation": "skills"}}}}
	before := Hash(b)
	b.Installations["skills"].Distribution["installation"] = "governed"
	if Hash(b) == before {
		t.Fatal("archive hash omits installation selection")
	}
}

func TestGovernedInstallationReusesRootInitial(t *testing.T) {
	b := &Bundle{SchemaVersion: 5, Initial: map[string]File{"AGENTS.md": encodedFile([]byte("formal"), 0644, "managed")}, Installations: map[string]Installation{"governed": {Distribution: map[string]any{"installation": "governed"}}}}
	view, err := b.ForInstallation("governed")
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Initial) != 1 {
		t.Fatal("lost original governed collection")
	}
	delete(view.Initial, "AGENTS.md")
	if len(b.Initial) != 1 {
		t.Fatal("mutated governed collection")
	}
}
