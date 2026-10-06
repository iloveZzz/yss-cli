package release_test

import (
	"path"
	"path/filepath"
	"testing"

	"github.com/iloveZzz/yss-cli/tools/release"
)

func TestNativeEnvelopeResultCompatibility(t *testing.T) {
	tests := []struct {
		name             string
		result           any
		version          bool
		status, code     string
		output, protocol int
		want             string
	}{
		{name: "skills-list-array", result: []any{map[string]any{"id": "tdd"}}, status: "ok", code: "OK", output: 1, protocol: 1},
		{name: "assets-list-empty-array", result: []any{}, status: "ok", code: "OK", output: 1, protocol: 1},
		{name: "null-generic-result", result: nil, status: "ok", code: "OK", output: 1, protocol: 1},
		{name: "object-generic-result", result: map[string]any{"installed": true}, status: "ok", code: "OK", output: 1, protocol: 1},
		{name: "failed-status-array", result: []any{}, status: "error", code: "OK", output: 1, protocol: 1, want: "EVIDENCE"},
		{name: "failed-code-null", result: nil, status: "ok", code: "CONFLICT", output: 1, protocol: 1, want: "EVIDENCE"},
		{name: "wrong-output-array", result: []any{}, status: "ok", code: "OK", output: 2, protocol: 1, want: "EVIDENCE"},
		{name: "wrong-protocol-array", result: []any{}, status: "ok", code: "OK", output: 1, protocol: 2, want: "EVIDENCE"},
		{name: "version-still-rejects-null", version: true, result: nil, status: "ok", code: "OK", output: 1, protocol: 1, want: "PROVENANCE"},
		{name: "version-still-binds-identity", version: true, result: map[string]any{"version": "1.0.1", "protocolVersion": 1, "cliCommit": commit, "sourceState": "committed"}, status: "ok", code: "OK", output: 1, protocol: 1, want: "PROVENANCE"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.input.RequiredPlatforms = []string{"darwin/arm64"}
			f.expected.RequiredPlatforms = []string{"darwin/arm64"}
			f.input.QualificationScope = release.LocalQualificationScope
			f.expected.QualificationScope = release.LocalQualificationScope
			f.input.Artifacts = []release.Artifact{f.input.Artifacts[1]}
			f.input.ReleaseGate = release.FileRef{}
			envelope := map[string]any{"outputVersion": tc.output, "protocolVersion": tc.protocol, "status": tc.status, "code": tc.code, "result": tc.result}
			editReceipt(t, f, 0, func(receipt *release.Receipt) {
				if tc.version {
					receipt.Version = f.json(t, receipt.Version.Path, envelope)
					receipt.VersionCommand.Stdout = receipt.Version
					return
				}
				editRaw(t, f, &receipt.Checks[0].Report, func(raw map[string]any) {
					for _, value := range raw["results"].([]any) {
						row := value.(map[string]any)
						command := row["command"].([]any)
						if len(command) == 2 && command[0] == "skills" && command[1] == "list" {
							ref := f.json(t, path.Join(path.Dir(receipt.Checks[0].Report.Path), "generic-result.json"), envelope)
							row["envelopeRef"] = path.Base(ref.Path)
							row["sha256"] = ref.SHA256
							return
						}
					}
					t.Fatal("fixture lacks real skills list public surface")
				})
			})
			err := release.AssembleCandidate(f.manifest(t), filepath.Join(f.dir, "out"), f.expected)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("legitimate generic result rejected: %v", err)
				}
			} else if release.Code(err) != tc.want {
				t.Fatalf("want %s got %v", tc.want, err)
			}
		})
	}
}
