package release

import (
	"encoding/json"
	"path"
	"reflect"
	"strings"
)

func pluginLabels() map[string]int {
	out := map[string]int{}
	for _, label := range []string{"go-head", "go-tree", "template-head", "template-clean", "go-before-tracked", "go-before-files", "template-before-tracked", "template-before-files", "binary-version", "python-jsonschema", "go-after-tracked", "go-after-files", "template-after-tracked", "template-after-files"} {
		out[label] = 1
	}
	for _, p := range []string{"spec", "design"} {
		for _, name := range []string{"inspect", "build", "staged-inspect", "verify", "doctor", "preview", "apply", "binding-check", "readonly-project-recover", "readonly-project-rollback", "whole-rollback"} {
			out[p+"-"+name] = 1
		}
		out[p+"-repeat-project-rollback"] = 2
		out[p+"-repeat-project-recover"] = 2
	}
	return out
}

func fileDescriptor(value any, readonly bool) bool {
	v, ok := value.(map[string]any)
	if !ok || v["type"] != "file" {
		return false
	}
	hash, ok := v["sha256"].(string)
	mode, valid := v["mode"].(float64)
	return ok && shaPattern.MatchString(hash) && valid && mode >= 0 && mode <= 0777 && mode == float64(uint32(mode)) && (!readonly || uint32(mode) == 0444)
}

func (r *reader) pluginReport(ref string, raw []byte, platform, binaryHash string) error {
	var report struct {
		Schema   int    `json:"schemaVersion"`
		Kind     string `json:"kind"`
		Status   string `json:"status"`
		Platform struct {
			OS   string `json:"os"`
			Arch string `json:"arch"`
		} `json:"platform"`
		Hash             string `json:"binary_sha256"`
		AfterHash        string `json:"binary_after_sha256"`
		Drift            *bool  `json:"input_drift"`
		BinaryDrift      *bool  `json:"binary_drift"`
		Error            any    `json:"error"`
		ObservationError any    `json:"observation_error"`
		BinaryError      any    `json:"binary_observation_error"`
		Head             string `json:"go_head"`
		TemplateHead     string `json:"template_head"`
		LockHash         string `json:"source_lock_sha256"`
		Version          struct {
			Version  string `json:"version"`
			Protocol int    `json:"protocolVersion"`
			Commit   string `json:"cliCommit"`
			State    string `json:"sourceState"`
		} `json:"binary_version"`
		Before map[string]any `json:"inputs_before"`
		After  map[string]any `json:"inputs_after"`
		Cases  []struct {
			Profile  string           `json:"profile"`
			Plugin   string           `json:"plugin"`
			Status   string           `json:"status"`
			PlanID   string           `json:"init_plan_id"`
			Metadata any              `json:"installed_metadata"`
			Binding  any              `json:"installed_binding"`
			Before   []map[string]any `json:"business_before"`
			After    []map[string]any `json:"business_after"`
			Preview  bool             `json:"readonly_preview"`
			Readonly bool             `json:"readonly_recovery"`
			Rollback bool             `json:"whole_atomic_rollback"`
			Repeat   bool             `json:"repeated_recovery"`
		} `json:"cases"`
		Commands []struct {
			Label      string   `json:"label"`
			Executable string   `json:"executable"`
			Args       []string `json:"args"`
			Observed   bool     `json:"exit_observed"`
			Exit       *int     `json:"exit_code"`
			Signal     any      `json:"signal"`
			Error      any      `json:"error"`
			Stdout     struct {
				File string `json:"file"`
				SHA  string `json:"sha256"`
			} `json:"stdout"`
			Stderr struct {
				File string `json:"file"`
				SHA  string `json:"sha256"`
			} `json:"stderr"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(raw, &report); err != nil || report.Schema != 1 || report.Kind != "native-plugin-public-smoke" || report.Status != "passed" || report.Hash != binaryHash || report.AfterHash != binaryHash || report.Drift == nil || *report.Drift || report.BinaryDrift == nil || *report.BinaryDrift || report.Error != nil || report.ObservationError != nil || report.BinaryError != nil {
		return reject("EVIDENCE", "invalid native plugin execution")
	}
	osname, arch := report.Platform.OS, report.Platform.Arch
	if osname == "win32" {
		osname = "windows"
	}
	if arch == "x64" {
		arch = "amd64"
	}
	if osname+"/"+arch != platform {
		return reject("EVIDENCE", "plugin runner platform mismatch")
	}
	if report.Head != r.identity.CLICommit || report.TemplateHead != r.identity.Bundles["spec"].TemplateCommit || report.LockHash != r.identity.SourceLockSHA256 || report.Version.Version != r.identity.CLIVersion || report.Version.Protocol != 1 || report.Version.Commit != r.identity.CLICommit || report.Version.State != "committed" {
		return reject("PROVENANCE", "plugin source identity mismatch")
	}
	if len(report.Before) != 2 || !reflect.DeepEqual(report.Before, report.After) {
		return reject("EVIDENCE", "plugin source inventory missing or changed")
	}
	for _, key := range []string{"go", "template"} {
		v, ok := report.Before[key].(map[string]any)
		if !ok {
			return reject("EVIDENCE", "missing plugin source inventory")
		}
		h, ok := v["sha256"].(string)
		entries, eok := v["inventory"].([]any)
		count, cok := v["files"].(float64)
		if !ok || !shaPattern.MatchString(h) || !eok || !cok || len(entries) == 0 || count != float64(len(entries)) {
			return reject("EVIDENCE", "incomplete plugin source inventory")
		}
	}
	seen := map[string]bool{}
	for _, c := range report.Cases {
		want := map[string]string{"spec": "yss-backend-delivery", "design": "yss-product-design"}[c.Profile]
		if want == "" || seen[c.Profile] || c.Plugin != want || c.Status != "passed" || c.PlanID == "" || !c.Preview || !c.Readonly || !c.Rollback || !c.Repeat || !fileDescriptor(c.Metadata, false) || !fileDescriptor(c.Binding, false) || len(c.Before) != 2 || !reflect.DeepEqual(c.Before, c.After) {
			return reject("EVIDENCE", "incomplete public plugin case assertions")
		}
		seen[c.Profile] = true
		protected := map[string]bool{}
		for _, file := range c.Before {
			name, _ := file["ref"].(string)
			if (name != "src/user-owned/readonly.txt" && name != ".github/workflows/user-owned-readonly.yml") || protected[name] || !fileDescriptor(file, true) {
				return reject("EVIDENCE", "plugin readonly business protection missing")
			}
			protected[name] = true
		}
	}
	if len(seen) != 2 || len(report.Commands) != 44 {
		return reject("EVIDENCE", "full 44-command public plugin acceptance required")
	}
	labels := pluginLabels()
	for _, row := range report.Commands {
		if labels[row.Label] <= 0 || row.Executable == "" || len(row.Args) == 0 || !row.Observed || row.Exit == nil || *row.Exit != 0 || row.Signal != nil || row.Error != nil {
			return reject("EVIDENCE", "failed, missing or duplicate plugin command %s", row.Label)
		}
		labels[row.Label]--
		for _, log := range []struct {
			File string
			SHA  string
		}{{row.Stdout.File, row.Stdout.SHA}, {row.Stderr.File, row.Stderr.SHA}} {
			if path.Base(log.File) != log.File || log.File == "." || strings.ContainsAny(log.File, "\\:") {
				return reject("PATH", "unsafe plugin log")
			}
			if _, err := r.file(FileRef{Path: path.Join(path.Dir(ref), log.File), SHA256: log.SHA}); err != nil {
				return err
			}
		}
	}
	return nil
}
