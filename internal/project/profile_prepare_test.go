package project

import (
	"encoding/json"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSavedProfilesPlanRejectsDesign(t *testing.T) {
	source, target := t.TempDir(), t.TempDir()
	p := &ProfilesPlan{SchemaVersion: 1, ProtocolVersion: domain.ProtocolVersion, Kind: "profile-prepare", SourceRoot: source, SourceProfile: "spec", SourceRefs: profileIdentityRefs("spec"), SourceInputs: map[string]domain.Descriptor{}, Links: ProfileLinks{SchemaVersion: 1, Links: map[string]string{"backend": target}}, LinksBefore: domain.Descriptor{Type: "missing"}, Targets: []ProfileTargetPlan{{Profile: "backend", Root: target, Action: "reuse", Inputs: map[string]domain.Descriptor{}}}, Changes: []ProfileLinkChange{{Profile: "backend", TargetRoot: target}}}
	for _, ref := range p.SourceRefs {
		p.SourceInputs[ref] = domain.Descriptor{Type: "missing"}
	}
	for _, ref := range profileIdentityRefs("backend") {
		p.Targets[0].Inputs[ref] = domain.Descriptor{Type: "missing"}
	}
	data, err := jsonBytes(p.Links)
	if err != nil {
		t.Fatal(err)
	}
	p.LinksAfter = domain.Descriptor{Type: "file", Mode: 0644, Digest: safefs.Digest(data)}
	p.Digest = profilesPlanDigest(p)
	if err = validateProfilesPlan(p); err != nil {
		t.Fatal("valid Backend plan", err)
	}
	p.SourceProfile = "design"
	p.Digest = profilesPlanDigest(p)
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(source, "saved.json")
	if err = os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = ReadProfilesPlan(file); err == nil || !strings.Contains(err.Error(), "来源或目标") {
		t.Fatal("removed Design source must be rejected before replay", err)
	}
	after, err := os.ReadFile(file)
	if err != nil || string(after) != string(raw) {
		t.Fatal("saved plan changed", err)
	}
}
