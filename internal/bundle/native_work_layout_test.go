package bundle

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func TestEmbeddedLifecycleProfilesKeepTheirContracts(t *testing.T) {
	contracts := map[string][]string{
		"design":   {"STRATEGIC_PROFILE_ID", "PROFILE_NEXT_ROUTES", "assertStrategicWorkUnitDecision", "export function validateTicketFormalization"},
		"backend":  {"export function validateSliceContractReadiness", "export function validateTechnicalDesignCompletion", `"work-unit.harness-entry": ["work-unit.technical-design"`},
		"frontend": {"export function validateSliceContractReadiness", `"work-unit.frontend-engineering-design"`},
	}
	for profile, required := range contracts {
		t.Run(profile, func(t *testing.T) {
			b, err := Load(profile)
			if err != nil {
				t.Fatal(err)
			}
			for variant, files := range map[string]map[string]File{"full": b.Files, "initial": b.Initial} {
				data, err := base64.StdEncoding.DecodeString(files["scripts/lib/lifecycle-transition.mjs"].Data)
				if err != nil {
					t.Fatal(err)
				}
				for _, contract := range required {
					if !strings.Contains(string(data), contract) {
						t.Errorf("%s lost Profile contract: %s", variant, contract)
					}
				}
				if profile == "design" && strings.Contains(string(data), `from "./backend-review.mjs"`) {
					t.Errorf("%s expanded strategic lifecycle into backend review", variant)
				}
			}
		})
	}
}

func TestEmbeddedWorkLayoutConsumers(t *testing.T) {
	expected, err := nativeWorkLayoutAssets.ReadFile("work-layout-assets/scripts/lib/work-layout.mjs")
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range []string{"spec", "design", "backend", "frontend"} {
		t.Run(profile, func(t *testing.T) {
			b, e := Load(profile)
			if e != nil {
				t.Fatal(e)
			}
			central, ok := b.Initial["scripts/lib/work-layout.mjs"]
			if !ok {
				t.Fatal("initial project lacks configured layout resolver")
			}
			raw, e := base64.StdEncoding.DecodeString(central.Data)
			if e != nil || !bytes.Equal(raw, expected) || central.Digest != b.Files["scripts/lib/work-layout.mjs"].Digest {
				t.Fatal("initial/full layout module differs from canonical compatibility source")
			}
			if !strings.Contains(string(raw), "tracker.root") {
				t.Fatal("missing tracker authority")
			}
			ignore, e := b.Initial[".gitignore"].Render(nil)
			if e != nil || strings.Contains(string(ignore), "# Template-source only") || strings.Contains("\n"+string(ignore), "\n.work/\n") {
				t.Fatalf("source-only work ignore distributed: %s %v", ignore, e)
			}
			for ref, f := range b.Files {
				if strings.HasPrefix(ref, ".work/") {
					t.Fatal("instance assets distributed: " + ref)
				}
				if strings.HasSuffix(ref, "/stage-tracking.mjs") || strings.HasSuffix(ref, "/reading-view-policy.mjs") || strings.HasSuffix(ref, "/offline-html.mjs") {
					raw, e := base64.StdEncoding.DecodeString(f.Data)
					if e != nil {
						t.Fatal("invalid production consumer: " + ref)
					}
					if strings.HasSuffix(ref, "/stage-tracking.mjs") && !strings.Contains(string(raw), "work-layout.mjs") {
						helperRef := strings.TrimSuffix(ref, "stage-tracking.mjs") + "reading-view-policy.mjs"
						helper, exists := b.Files[helperRef]
						helperRaw, err := base64.StdEncoding.DecodeString(helper.Data)
						if !exists || err != nil || !strings.Contains(string(raw), "from './reading-view-policy.mjs'") || !strings.Contains(string(raw), "readingLocation(") || !strings.Contains(string(helperRaw), "from './work-layout.mjs'") || !strings.Contains(string(helperRaw), "readWorkLayout(") {
							t.Fatal("stage consumer lacks its actual same-directory layout dependency: " + ref)
						}
						continue
					}
					if !strings.Contains(string(raw), "work-layout.mjs") {
						t.Fatal("unadapted production consumer: " + ref)
					}
				}
			}
			for _, tr := range b.NativeTransforms {
				if tr.Path == "scripts/lib/work-layout.mjs" {
					if tr.Generator != "native-work-layout-v1" || tr.OutputDigest != central.Digest || tr.Source.Digest == central.Digest {
						t.Fatal("layout compatibility transform lost source/output provenance")
					}
					if tr.SourceAbsent && tr.Source.Digest != safefs.Digest(nil) {
						t.Fatal("absent layout source has nonempty original bytes")
					}
				}
			}
		})
	}
}

func TestNativeWorkLayoutSourceProvenance(t *testing.T) {
	const ref = "scripts/lib/work-layout.mjs"
	expected, err := nativeWorkLayoutAssets.ReadFile("work-layout-assets/" + ref)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name       string
		original   []byte
		exists     bool
		transforms bool
	}{
		{name: "old-source-without-module", transforms: true},
		{name: "canonical-module-already-in-source", original: expected, exists: true},
		{name: "older-module-in-source", original: []byte("export const workLayout = {};\n"), exists: true, transforms: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := map[string]sourceFile{}
			if tc.exists {
				raw[ref] = sourceFile{data: tc.original, mode: 0644}
			}
			transforms, err := nativeWorkLayoutSource(raw)
			if err != nil || !bytes.Equal(raw[ref].data, expected) {
				t.Fatalf("layout output differs: %v", err)
			}
			count := 0
			for _, tr := range transforms {
				if tr.Path != ref {
					continue
				}
				count++
				original, err := tr.Source.Render(nil)
				if err != nil || !bytes.Equal(original, tc.original) || tr.SourceAbsent != !tc.exists || tr.Generator != "native-work-layout-v1" {
					t.Fatalf("layout source provenance differs: %+v %v", tr, err)
				}
			}
			want := 0
			if tc.transforms {
				want = 1
			}
			if count != want {
				t.Fatalf("layout transform count = %d, want %d", count, want)
			}
		})
	}
}
