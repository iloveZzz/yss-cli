package release

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"time"
)

var profiles = []string{"spec", "backend"}
var integrationCases = []string{"init", "doctor", "diff", "sync-dry-run", "sync-apply", "migration-plan-read-only", "migration-apply", "idempotency", "git-baseline", "user-file-preservation"}
var windowsAbsolutePath = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

// RequiredGateCoverage is a fixed public contract, not an input-selected subset.
func RequiredGateCoverage(id string) []string {
	switch id {
	case "program-installation":
		return []string{"install-plan-read-only", "install-apply", "reinstall", "user-file-preservation"}
	case "scoped-template-consumers":
		return append([]string(nil), scopedConsumerCases...)
	case "full-template-integration":
		return []string{"template.release.complete-candidate", "template.release.committed-sources", "template.release.supported-cli-integrations"}
	case "cli-integration":
		return []string{"cli.integration.spec", "cli.integration.backend"}

	}
	return nil
}
func sameSet(actual, expected []string) bool {
	a, b := append([]string(nil), actual...), append([]string(nil), expected...)
	sort.Strings(a)
	sort.Strings(b)
	return actual != nil && reflect.DeepEqual(a, b)
}
func object(value any) map[string]any { v, _ := value.(map[string]any); return v }
func items(value any) []any           { v, _ := value.([]any); return v }
func text(value any) string           { v, _ := value.(string); return v }
func stringsOf(value any) []string {
	a, ok := value.([]any)
	if !ok {
		return nil
	}
	out := []string{}
	for _, v := range a {
		s, ok := v.(string)
		if !ok {
			return nil
		}
		out = append(out, s)
	}
	return out
}
func empty(value any) bool { a, ok := value.([]any); return ok && len(a) == 0 }
func absentFailure(o map[string]any) bool {
	for _, k := range []string{"error", "failure", "storageError", "observation_error", "source_guard_failure"} {
		if v, ok := o[k]; ok && v != nil && v != "" && v != false {
			return false
		}
	}
	return true
}
func parseObject(raw []byte) (map[string]any, error) {
	var o map[string]any
	if err := json.Unmarshal(raw, &o); err != nil || o == nil {
		return nil, reject("EVIDENCE", "invalid raw report JSON")
	}
	return o, nil
}
func doneTimes(o map[string]any) bool {
	a, e := time.Parse(time.RFC3339Nano, text(o["started_at"]))
	b, f := time.Parse(time.RFC3339Nano, text(o["finished_at"]))
	return e == nil && f == nil && !b.Before(a)
}

type gateSources struct {
	byKind map[string][]byte
	byHash map[string][]byte
}

func (s gateSources) dependency(hash any) ([]byte, error) {
	h := text(hash)
	raw, ok := s.byHash[h]
	if !shaPattern.MatchString(h) || !ok {
		return nil, reject("EVIDENCE", "raw evidence dependency missing: %s", h)
	}
	return raw, nil
}
func (r *reader) gateReport(id string, raw []byte) error {
	var gate GateExecution
	if err := decode(raw, &gate); err != nil {
		return err
	}
	if gate.SchemaVersion != 1 || gate.Kind != "stable-release-gate-execution" || gate.GateID != id || gate.QualificationScope != r.expected.QualificationScope || !bytes.Equal(bytes.TrimSpace(gate.Signal), []byte("null")) || !bytes.Equal(bytes.TrimSpace(gate.Error), []byte("null")) {
		return reject("EVIDENCE", "invalid observed gate execution")
	}
	if err := validIdentity(gate.Identity, r.identity); err != nil {
		return err
	}
	if err := passed(gate.Status, gate.ExitCode, gate.InputDrift, gate.Unexecuted); err != nil {
		return err
	}
	if r.binaries[gate.Platform] == "" || r.binaries[gate.Platform] != gate.BinarySHA256 || !sameSet(gate.Coverage, RequiredGateCoverage(id)) {
		return reject("EVIDENCE", "gate platform, binary or required coverage mismatch")
	}
	s := gateSources{byKind: map[string][]byte{}, byHash: map[string][]byte{}}
	for _, source := range gate.SourceReports {
		b, err := r.file(source.Report)
		if err != nil {
			return err
		}
		s.byHash[source.Report.SHA256] = b
		if source.Kind == "real-original-before" || source.Kind == "real-original-after" || source.Kind == "real-protected-before" || source.Kind == "real-whole-restored" {
			if source.SchemaVersion != 0 {
				return reject("SCHEMA", "inventory schema must be zero")
			}
			if _, exists := s.byKind[source.Kind]; exists {
				return reject("EVIDENCE", "duplicate inventory")
			}
			if _, err = parseObject(b); err != nil {
				return err
			}
			s.byKind[source.Kind] = b
			continue
		}
		if source.Kind == "evidence-file" {
			if source.SchemaVersion != 0 {
				return reject("SCHEMA", "binary/log dependency is not a report")
			}
			continue
		}
		if _, exists := s.byKind[source.Kind]; exists {
			return reject("EVIDENCE", "duplicate original source report %s", source.Kind)
		}
		o, err := parseObject(b)
		if err != nil {
			return err
		}
		expectedSchema := 1
		if source.Kind == "template-release-verification" || source.Kind == "template-verification-report" || source.Kind == "template-release-sources" {
			expectedSchema = 2
		}
		if source.SchemaVersion != expectedSchema || o["schema_version"] != float64(expectedSchema) {
			return reject("SCHEMA", "source report schema mismatch")
		}
		s.byKind[source.Kind] = b
	}
	switch id {
	case "program-installation":
		return r.programGate(s, gate.Platform, gate.BinarySHA256)
	case "scoped-template-consumers":
		return r.scopedTemplateGate(s)
	case "full-template-integration", "cli-integration":
		return r.templateGate(s, gate.BinarySHA256)
	}
	return reject("EVIDENCE", "unknown release gate")
}

func (r *reader) sourceManifest(o map[string]any, binary string) error {
	if o["schema_version"] != float64(2) || o["kind"] != "template-release-sources" || o["root_commit"] != r.identity.Bundles["spec"].TemplateCommit || !sameSet(stringsOf(o["families"]), profiles) || len(items(o["entries"])) != len(profiles) {
		return reject("PROVENANCE", "invalid supported-profile source manifest")
	}
	seen := map[string]bool{}
	for _, v := range items(o["entries"]) {
		row := object(v)
		p := text(row["family"])
		b, ok := r.identity.Bundles[p]
		if !ok || seen[p] || row["namespace"] != "candidate-release" || row["package_name"] != "yss" || row["cli_commit"] != r.identity.CLICommit || row["core_commit"] != r.identity.Bundles["spec"].TemplateCommit || row["template_commit"] != b.TemplateCommit || row["version"] != r.identity.CLIVersion || row["template_version"] != b.TemplateVersion || row["source_contract_version"] != float64(2) || row["protocol_version"] != float64(1) || row["snapshot_hash"] != b.SourceSnapshotHash || row["manifest_hash"] != b.ManifestHash || row["bundle_hash"] != b.BundleHash || row["binary_sha256"] != binary {
			return reject("PROVENANCE", "native source tuple mismatch")
		}
		seen[p] = true
	}
	return nil
}
func rawCommand(row map[string]any, s gateSources) error {
	if row["actual_exit_code_observed"] != true || row["actual_exit_code"] != float64(0) || row["exit_code"] != float64(0) || row["actual_exit_signal"] != nil || !absentFailure(row) {
		return reject("EVIDENCE", "raw template command failed or unobserved")
	}
	if _, ok := row["actual_exit_signal"]; !ok {
		return reject("EVIDENCE", "raw command signal observation missing")
	}
	_, err := s.dependency(row["log_sha256"])
	return err
}

func nativeSyncCommands(commands []any, binary string) error {
	root, firstSync, plans, applies := "", len(commands), 0, 0
	for i, item := range commands {
		row := object(item)
		if row["command"] != binary {
			continue
		}
		args := stringsOf(row["args"])
		if len(args) == 0 {
			continue
		}
		if args[0] == "recover" || args[0] == "rollback" {
			return reject("EVIDENCE", "sync integration must not run recovery or rollback")
		}
		if args[0] != "sync" {
			continue
		}
		if i < firstSync {
			firstSync = i
		}
		found := ""
		for j, arg := range args {
			if arg == "--root" && j+1 < len(args) {
				if found != "" {
					return reject("EVIDENCE", "sync root repeated")
				}
				found = args[j+1]
			}
			if arg == "--plan" {
				plans++
			}
			if arg == "--apply" {
				applies++
			}
		}
		if found == "" || (!filepath.IsAbs(found) && !windowsAbsolutePath.MatchString(found)) || root != "" && root != found {
			return reject("EVIDENCE", "sync Git root missing or inconsistent")
		}
		root = found
	}
	if root == "" || plans < 3 || applies < 2 {
		return reject("EVIDENCE", "sync plans or applications missing")
	}
	init, commit := false, false
	for i, item := range commands {
		row := object(item)
		if i >= firstSync || row["cwd"] != root || row["command"] != "git" && row["file"] != "git" {
			continue
		}
		args := stringsOf(row["args"])
		for len(args) >= 2 && args[0] == "-c" {
			args = args[2:]
		}
		if len(args) > 0 && args[0] == "init" {
			init = true
		}
		if len(args) > 0 && args[0] == "commit" && init {
			commit = true
		}
	}
	if !init || !commit {
		return reject("EVIDENCE", "committed Git baseline not observed")
	}
	return nil
}
func (r *reader) templateGate(s gateSources, binary string) error {
	o, err := parseObject(s.byKind["template-release-verification"])
	if err != nil {
		return err
	}
	if o["schema_version"] != float64(2) || o["kind"] != "template-release-verification" || o["purpose"] != "verification" || o["status"] != "passed" || o["requested_commit"] != r.identity.Bundles["spec"].TemplateCommit || !sameSet(stringsOf(o["cli_families"]), profiles) || !doneTimes(o) || !absentFailure(o) {
		return reject("EVIDENCE", "invalid complete template release report")
	}
	fullDescriptor := object(o["full_verification"])
	fullRaw, err := s.dependency(fullDescriptor["report_sha256"])
	if err != nil {
		return err
	}
	if !bytes.Equal(fullRaw, s.byKind["template-verification-report"]) {
		return reject("EVIDENCE", "full verification report descriptor mismatch")
	}
	full, err := parseObject(fullRaw)
	if err != nil {
		return err
	}
	if err = r.fullVerification(full, s, binary); err != nil {
		return err
	}
	if fullDescriptor["status"] != "passed" || fullDescriptor["input_sha256"] != full["input_sha256"] {
		return reject("EVIDENCE", "full verification identity mismatch")
	}
	sourcesRaw, err := s.dependency(o["sources_manifest_sha256"])
	if err != nil {
		return err
	}
	if !bytes.Equal(sourcesRaw, s.byKind["template-release-sources"]) {
		return reject("EVIDENCE", "sources manifest descriptor mismatch")
	}
	sources, err := parseObject(sourcesRaw)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(sources, object(o["sources_manifest"])) {
		return reject("PROVENANCE", "outer source manifest differs from original source bytes")
	}
	if err = r.sourceManifest(sources, binary); err != nil {
		return err
	}
	commands := append([]any(nil), items(o["commands"])...)
	if len(commands) == 0 {
		return reject("EVIDENCE", "template release command evidence missing")
	}
	artifacts := items(o["artifacts"])
	if len(artifacts) != len(profiles) {
		return reject("EVIDENCE", "actual native artifacts for supported profiles required")
	}
	seen := map[string]bool{}
	artifactBins := map[string]string{}
	for _, a := range artifacts {
		art := object(a)
		tuple := object(art["source_tuple"])
		p := text(tuple["family"])
		if seen[p] || r.identity.Bundles[p].TemplateCommit == "" {
			return reject("PROVENANCE", "duplicate or unknown CLI artifact")
		}
		seen[p] = true
		if err = r.sourceManifest(map[string]any{"schema_version": float64(2), "kind": "template-release-sources", "root_commit": sources["root_commit"], "families": sources["families"], "entries": sources["entries"]}, binary); err != nil {
			return err
		}
		var source map[string]any
		for _, v := range items(sources["entries"]) {
			if object(v)["family"] == p {
				source = object(v)
			}
		}
		for _, key := range []string{"namespace", "family", "cli_commit", "template_commit", "core_commit", "package_name", "version", "snapshot_hash", "manifest_hash", "source_contract_version", "protocol_version", "template_version", "bundle_hash", "binary_sha256"} {
			if !reflect.DeepEqual(tuple[key], source[key]) {
				return reject("PROVENANCE", "native artifact tuple mismatch")
			}
		}
		if art["tarball_sha256"] != binary || art["snapshot_sha256"] != source["snapshot_hash"] || art["manifest_sha256"] != source["manifest_hash"] || !shaPattern.MatchString(text(art["installed_tree_sha256"])) {
			return reject("EVIDENCE", "native artifact provenance incomplete")
		}
		artifactBins[p] = text(art["binary"])
		if artifactBins[p] == "" {
			return reject("EVIDENCE", "missing executed artifact binary")
		}
		commands = append(commands, items(art["command_records"])...)
	}
	integrations := items(o["cli_integrations"])
	if len(integrations) != len(profiles) {
		return reject("EVIDENCE", "CLI integrations for supported profiles required")
	}
	seen = map[string]bool{}
	for _, v := range integrations {
		row := object(v)
		p := text(row["family"])
		if seen[p] || artifactBins[p] == "" || row["status"] != "passed" || row["migration_fixture"] != "native-managed-upgrade" || !sameSet(stringsOf(row["cases"]), integrationCases) || !absentFailure(row) {
			return reject("EVIDENCE", "CLI integration coverage incomplete")
		}
		seen[p] = true
		commands = append(commands, items(row["command_records"])...)
	}
	counts := map[string]map[string]int{}
	for _, p := range profiles {
		counts[p] = map[string]int{}
	}
	for _, v := range commands {
		row := object(v)
		if err = rawCommand(row, s); err != nil {
			return err
		}
		args := stringsOf(row["args"])
		for p, bin := range artifactBins {
			if row["command"] != bin || len(args) == 0 {
				continue
			}
			key := args[0]
			if key == "bundle" && len(args) > 1 {
				key += " " + args[1]
			}
			if key == "init" {
				for _, arg := range args {
					if arg == "--plan" || arg == "--apply" {
						key += " " + arg
						break
					}
				}
			}
			counts[p][key]++
		}
	}
	for _, p := range profiles {
		for _, key := range []string{"init --plan", "init --apply", "doctor", "diff", "sync", "bundle export"} {
			if counts[p][key] < 1 || (key == "bundle export" && counts[p][key] != 1) {
				return reject("EVIDENCE", "missing actual %s command for %s", key, p)
			}
		}
		if err := nativeSyncCommands(commands, artifactBins[p]); err != nil {
			return err
		}
	}
	return nil
}

func (r *reader) fullVerification(o map[string]any, s gateSources, binary string) error {
	plan := object(o["plan"])
	e := r.expected
	final := object(o["final_exit"])
	if o["schema_version"] != float64(2) || o["kind"] != "template-verification-report" || o["purpose"] != "verification" || o["status"] != "passed" || o["root"] != e.TemplateRoot || object(o["scope"])["kind"] != "complete-candidate" || plan["effective_profile"] != "release" || plan["source_requirement"] != "committed" || e.VerificationPlan == nil || e.VerificationInvocation == nil || !shaPattern.MatchString(e.VerificationInputSHA256) || !reflect.DeepEqual(o["invocation"], e.VerificationInvocation) || final["observed"] != true || final["code"] != float64(0) || final["signal"] != nil || o["input_drift"] != false || o["input_sha256"] != e.VerificationInputSHA256 || o["input_after_sha256"] != e.VerificationInputSHA256 || !empty(o["unexecuted"]) || !doneTimes(o) || !absentFailure(o) {
		return reject("EVIDENCE", "invalid independently bound full verification")
	}
	if _, ok := final["signal"]; !ok {
		return reject("EVIDENCE", "final signal observation missing")
	}
	for _, key := range []string{"effective_profile", "source_requirement", "strategy", "policy_digest", "commands", "gates"} {
		if !reflect.DeepEqual(plan[key], e.VerificationPlan[key]) {
			return reject("EVIDENCE", "full verification differs from independent plan: %s", key)
		}
	}
	if err := r.sourceManifest(object(o["sources_manifest"]), binary); err != nil {
		return err
	}
	tasks := []map[string]any{}
	for _, v := range items(plan["commands"]) {
		task := object(v)
		if task["when"] == nil || task["when"] == "template-source" {
			tasks = append(tasks, task)
		}
	}
	results := items(o["results"])
	if len(tasks) == 0 || len(tasks) != len(results) {
		return reject("EVIDENCE", "full result set is incomplete")
	}
	key := func(o map[string]any) string {
		if id := text(o["task_id"]); id != "" {
			return id
		}
		return fmt.Sprint(o["index"])
	}
	rows := map[string]map[string]any{}
	for _, v := range results {
		row := object(v)
		k := key(row)
		if rows[k] != nil {
			return reject("EVIDENCE", "duplicate full verification result")
		}
		rows[k] = row
	}
	for _, task := range tasks {
		row := rows[key(task)]
		if row == nil || row["command"] != task["command"] || row["code"] != float64(0) || row["skipped"] == true || !absentFailure(row) {
			return reject("EVIDENCE", "missing or failed full task")
		}
		if execution := object(task["execution"]); execution != nil {
			actual := object(row["actual_execution"])
			copy := map[string]any{}
			for k, v := range actual {
				if k != "source_receipt_sha256" {
					copy[k] = v
				}
			}
			if !reflect.DeepEqual(copy, execution) {
				return reject("EVIDENCE", "full task executed different argv/root/env")
			}
		}
		actual := row
		if row["reused"] == true {
			origin := object(row["reused_from"])
			actual = rows[key(origin)]
			if actual == nil || key(origin) == key(row) || actual["reused"] == true || actual["command"] != row["command"] || actual["stdoutFile"] != row["stdoutFile"] || actual["stderrFile"] != row["stderrFile"] {
				return reject("EVIDENCE", "full reuse lacks same-round actual origin")
			}
		}
		if actual["actual_exit_code_observed"] != true || actual["actual_exit_code"] != float64(0) || actual["actual_exit_signal"] != nil || !absentFailure(actual) {
			return reject("EVIDENCE", "full actual task exit failed or interrupted")
		}
		if _, ok := actual["actual_exit_signal"]; !ok {
			return reject("EVIDENCE", "full actual task signal missing")
		}
		for _, stream := range []string{"stdoutFile", "stderrFile"} {
			if text(row[stream]) == "" {
				return reject("EVIDENCE", "full task stream missing")
			}
			if _, err := s.dependency(object(row["log_digests"])[stream]); err != nil {
				return err
			}
		}
	}
	conclusions := items(o["gate_results"])
	for _, v := range items(plan["gates"]) {
		gate := object(v)
		if gate["selected"] != true {
			continue
		}
		expected := []string{}
		for _, task := range tasks {
			for _, id := range stringsOf(task["gate_ids"]) {
				if id == gate["id"] {
					name := text(task["task_id"])
					if name == "" {
						name = text(task["id"])
					}
					expected = append(expected, name)
				}
			}
		}
		found := false
		for _, v := range conclusions {
			row := object(v)
			if row["id"] == gate["id"] {
				if found || row["status"] != "passed" || len(expected) == 0 || !sameSet(stringsOf(row["task_ids"]), expected) {
					return reject("EVIDENCE", "selected Gate result incomplete")
				}
				found = true
			}
		}
		if !found {
			return reject("EVIDENCE", "selected Gate result missing")
		}
	}
	return nil
}
