package project

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

func TestPreflightApplyIsReadOnlyAndRejectsOccupiedInitTarget(t *testing.T) {
	root := freshRoot(t)
	p, err := Build(root, "backend", "init", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = PreflightApplyContext(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("preflight created its target")
	}
	if err = os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "business.txt")
	if err = os.WriteFile(file, []byte("business"), 0644); err != nil {
		t.Fatal(err)
	}
	if err = PreflightApplyContext(context.Background(), p); err == nil {
		t.Fatal("occupied target accepted by preflight")
	}
	if raw, err := os.ReadFile(file); err != nil || string(raw) != "business" {
		t.Fatal("preflight changed business bytes")
	}
}

func profilePreparationFixture(t *testing.T) (string, ProfileTargets) {
	t.Helper()
	source := freshRoot(t)
	p, err := Build(source, "design", "init", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Apply(p); err != nil {
		t.Fatal(err)
	}
	base := filepath.Dir(source)
	return source, ProfileTargets{"backend": filepath.Join(base, "backend"), "frontend": filepath.Join(base, "frontend")}
}

func TestProfilesPreparePreflightsEveryTargetBeforeRegistration(t *testing.T) {
	source, targets := profilePreparationFixture(t)
	p, err := PrepareProfiles(context.Background(), source, targets, nil, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(targets["frontend"], 0755); err != nil {
		t.Fatal(err)
	}
	business := filepath.Join(targets["frontend"], "business.txt")
	if err = os.WriteFile(business, []byte("preserve"), 0644); err != nil {
		t.Fatal(err)
	}
	r, err := ApplyProfiles(context.Background(), p, nil)
	if err == nil || r.Status != "failed" || r.Targets[0].Status != "not-executed" || r.Targets[1].Status != "failed" {
		t.Fatalf("preflight result: %+v %v", r, err)
	}
	for _, file := range []string{filepath.Join(source, ProfileLinksFile), targets["backend"]} {
		if _, err = os.Stat(file); !os.IsNotExist(err) {
			t.Fatalf("preflight wrote %s", file)
		}
	}
	if raw, _ := os.ReadFile(business); string(raw) != "preserve" {
		t.Fatal("business content changed")
	}
}

func TestProfilesPreparePartialFailureKeepsSuccessAndSavedPlanCanRetry(t *testing.T) {
	source, targets := profilePreparationFixture(t)
	p, err := PrepareProfiles(context.Background(), source, targets, nil, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	r, err := ApplyProfilesWithOptions(context.Background(), p, ProfileApplyOptions{BeforeTarget: func(_ context.Context, target ProfileTargetPlan) error {
		if target.Profile == "frontend" {
			if _, err := os.Stat(filepath.Join(source, ProfileLinksFile)); err == nil {
				return domain.Fail("TRANSFER_FAILED", "frontend handoff failed")
			}
		}
		return nil
	}})
	if err == nil || r.Status != "partial" || r.Targets[0].Status != "success" || r.Targets[1].Status != "failed" || r.Targets[1].Code != "TRANSFER_FAILED" {
		t.Fatalf("partial result: %+v %v", r, err)
	}
	if _, err = Detect(targets["backend"], "backend", false); err != nil {
		t.Fatal("successful target was removed")
	}
	if links, err := ReadProfileLinks(source); err != nil || links.Links["frontend"] != targets["frontend"] {
		t.Fatal("partial result lost its registered intent")
	}
	before, err := transaction.Status(targets["backend"])
	if err != nil {
		t.Fatal(err)
	}
	r, err = ApplyProfiles(context.Background(), p, nil)
	if err != nil || r.Status != "completed" || r.Targets[0].Action != "reuse" {
		t.Fatalf("retry: %+v %v", r, err)
	}
	after, err := transaction.Status(targets["backend"])
	if err != nil || len(after.Transactions) != len(before.Transactions) {
		t.Fatal("retry reinitialized successful target")
	}
}

func TestProfilesPrepareSavedDualPlanAndRepeatReuse(t *testing.T) {
	source := freshRoot(t)
	initial, err := Build(source, "design", "init", map[string]string{"projectName": "填报"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Apply(initial); err != nil {
		t.Fatal(err)
	}
	base := filepath.Dir(source)
	targets := ProfileTargets{"backend": filepath.Join(base, "backend"), "frontend": filepath.Join(base, "frontend")}
	p, err := PrepareProfiles(context.Background(), source, targets, nil, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Changes) != 2 || p.Changes[0].Profile != "backend" || p.Changes[0].PreviousRoot != "" || p.Changes[0].TargetRoot != targets["backend"] {
		t.Fatalf("plan old and new roots missing: %+v", p.Changes)
	}
	for _, root := range targets {
		if _, err = os.Stat(root); !os.IsNotExist(err) {
			t.Fatal("prepare wrote target")
		}
	}
	if _, err = os.Stat(filepath.Join(source, ProfileLinksFile)); !os.IsNotExist(err) {
		t.Fatal("prepare registered links")
	}
	file := filepath.Join(base, "profiles-plan.json")
	if err = SaveProfilesPlan(p, file); err != nil {
		t.Fatal(err)
	}
	p, err = ReadProfilesPlan(file)
	if err != nil {
		t.Fatal(err)
	}
	r, err := ApplyProfiles(context.Background(), p, nil)
	if err != nil || r.Status != "completed" {
		t.Fatalf("apply: %+v %v", r, err)
	}
	links, err := ReadProfileLinks(source)
	if err != nil || links.Links["backend"] != targets["backend"] || links.Links["frontend"] != targets["frontend"] {
		t.Fatalf("registered paths: %+v %v", links, err)
	}
	for name, root := range targets {
		id, err := Detect(root, name, false)
		if err != nil || id.Native == nil {
			t.Fatalf("target identity: %s %v", name, err)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(source, ProfileLinksFile))
	var record map[string]any
	if err = json.Unmarshal(raw, &record); err != nil || len(record) != 2 || record["schema_version"] != float64(1) {
		t.Fatalf("links persisted execution state: %s %v", raw, err)
	}
	p, err = PrepareProfiles(context.Background(), source, targets, nil, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range p.Targets {
		if target.Action != "reuse" || target.Init != nil {
			t.Fatalf("repeat reinitializes target: %+v", target)
		}
	}
	if _, err = ApplyProfiles(context.Background(), p, nil); err != nil {
		t.Fatal(err)
	}
}

func TestProfilesPrepareSourceEvidenceDriftAndRegistrationRollback(t *testing.T) {
	source, targets := profilePreparationFixture(t)
	proof := filepath.Join(source, "proof.json")
	if err := os.WriteFile(proof, []byte("approved"), 0644); err != nil {
		t.Fatal(err)
	}
	p, err := PrepareProfiles(context.Background(), source, targets, nil, "proof.json", []string{"proof.json", ".yss/transactions", ".yss/asset-transactions/old"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.SourceCheckpoint != "proof.json" || p.Public()["source_checkpoint"] != "proof.json" {
		t.Fatal("explicit source checkpoint lost")
	}
	for _, ref := range []string{".yss/transactions", ".yss/asset-transactions/old"} {
		if _, ok := p.SourceInputs[ref]; ok {
			t.Fatal("runtime controls persisted as source approval facts")
		}
	}
	assertUnwritten := func() {
		t.Helper()
		for _, file := range []string{filepath.Join(source, ProfileLinksFile), targets["backend"], targets["frontend"]} {
			if _, err := os.Stat(file); !os.IsNotExist(err) {
				t.Fatalf("preflight changed %s", file)
			}
		}
	}
	if err = os.WriteFile(proof, []byte("revoked"), 0644); err != nil {
		t.Fatal(err)
	}
	r, err := ApplyProfiles(context.Background(), p, nil)
	if err == nil || r.Code != "INPUT_DRIFT" {
		t.Fatalf("source byte drift accepted: %+v %v", r, err)
	}
	assertUnwritten()
	if err = os.WriteFile(proof, []byte("approved"), 0644); err != nil {
		t.Fatal(err)
	}
	revoked := domain.Fail("SOURCE_REVOKED", "source approval revoked")
	r, err = ApplyProfiles(context.Background(), p, func(context.Context, string) error { return revoked })
	if !errors.Is(err, revoked) || r.Code != "SOURCE_REVOKED" {
		t.Fatalf("source authorization failure lost: %+v %v", r, err)
	}
	if len(r.RecoveryCommands) == 0 {
		t.Fatal("failure omitted recovery command")
	}
	assertUnwritten()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	r, err = ApplyProfiles(cancelled, p, nil)
	if err == nil || r.Code != "CANCELLED" {
		t.Fatalf("cancelled apply accepted: %+v %v", r, err)
	}
	assertUnwritten()
	r, err = ApplyProfilesWithOptions(context.Background(), p, ProfileApplyOptions{BeforeTarget: func(_ context.Context, target ProfileTargetPlan) error {
		if target.Profile == "backend" {
			if _, err := os.Stat(filepath.Join(source, ProfileLinksFile)); err == nil {
				return os.WriteFile(proof, []byte("revoked"), 0644)
			}
		}
		return nil
	}})
	if err == nil || r.Status != "partial" || r.Code != "INPUT_DRIFT" || r.Targets[0].Status != "failed" || r.Targets[1].Status != "not-executed" {
		t.Fatalf("source drift after registration: %+v %v", r, err)
	}
	for _, root := range targets {
		if _, err = os.Stat(root); !os.IsNotExist(err) {
			t.Fatal("source mutation did not stop target write")
		}
	}
	if _, err = transaction.RollbackKindContextWithValidator(context.Background(), source, ProfileLinksKind, func(_ transaction.Summary, refs []string) error {
		return ValidateProfileLinksTransaction(source, "design", refs)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(source, ProfileLinksFile)); !os.IsNotExist(err) {
		t.Fatal("registration rollback retained links")
	}
	if raw, _ := os.ReadFile(proof); string(raw) != "revoked" {
		t.Fatal("registration rollback restored unrelated source evidence")
	}
	for _, refs := range [][]string{nil, {"CONTEXT.md"}, {ProfileLinksFile, "other"}} {
		if err = ValidateProfileLinksTransaction(source, "design", refs); err == nil {
			t.Fatalf("archive scope accepted: %v", refs)
		}
	}
}

func TestProfilesPlanChangesCannotAlterOldOrUnselectedRoots(t *testing.T) {
	source := freshRoot(t)
	root := filepath.Join(filepath.Dir(source), "backend")
	missing := domain.Descriptor{Type: "missing"}
	inputs := func(profile string) map[string]domain.Descriptor {
		guards := map[string]domain.Descriptor{}
		for _, ref := range profileIdentityRefs(profile) {
			guards[ref] = missing
		}
		return guards
	}
	p := &ProfilesPlan{SchemaVersion: 1, ProtocolVersion: domain.ProtocolVersion, Kind: "profile-prepare", SourceRoot: source, SourceProfile: "design", SourceRefs: profileIdentityRefs("design"), SourceInputs: inputs("design"), Targets: []ProfileTargetPlan{{Profile: "backend", Root: root, Action: "reuse", Inputs: inputs("backend")}}, Changes: []ProfileLinkChange{{Profile: "backend", TargetRoot: root}}, LinksBefore: missing, Links: ProfileLinks{SchemaVersion: 1, Links: map[string]string{"backend": root}}}
	rehash := func() {
		raw, err := jsonBytes(p.Links)
		if err != nil {
			t.Fatal(err)
		}
		p.LinksAfter = domain.Descriptor{Type: "file", Digest: safefs.Digest(raw), Mode: 0644}
		p.Digest = profilesPlanDigest(p)
	}
	rehash()
	if err := validateProfilesPlan(p); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(filepath.Dir(source), "plan.json")
	if err := SaveProfilesPlan(p, file); err != nil {
		t.Fatal(err)
	}
	if read, err := ReadProfilesPlan(file); err != nil || read.Changes[0].TargetRoot != root || read.Public()["changes"] == nil {
		t.Fatalf("change summary lost: %+v %v", read, err)
	}
	p.Changes[0].PreviousRoot = filepath.Join(filepath.Dir(source), "previous-backend")
	rehash()
	if err := validateProfilesPlan(p); err == nil {
		t.Fatal("changed and rehashed old association accepted")
	}
	p.Changes[0].PreviousRoot = ""
	p.Changes[0].TargetRoot = filepath.Join(filepath.Dir(source), "another-backend")
	rehash()
	if err := validateProfilesPlan(p); err == nil {
		t.Fatal("change summary disagrees with execution target")
	}
	p.Changes[0].TargetRoot = root
	p.Links.Links["frontend"] = filepath.Join(filepath.Dir(source), "unselected-frontend")
	rehash()
	if err := validateProfilesPlan(p); err == nil {
		t.Fatal("unselected new association accepted")
	}
}
