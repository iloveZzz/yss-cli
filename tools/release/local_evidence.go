package release

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

// PayloadSHA256 binds the unchanged installable bytes, excluding the manifest
// whose qualification proof is added only after actual candidate installation.
func PayloadSHA256(platform, binarySHA string, documents map[string]string) string {
	refs := make([]string, 0, len(documents)+1)
	hashes := map[string]string{}
	for ref, hash := range documents {
		refs = append(refs, ref)
		hashes[ref] = hash
	}
	binaryRef := "yss"
	if strings.HasPrefix(platform, "windows/") {
		binaryRef = "yss.exe"
	}
	refs = append(refs, binaryRef)
	hashes[binaryRef] = binarySHA
	sort.Strings(refs)
	var data strings.Builder
	for _, ref := range refs {
		mode := 0644
		if ref == binaryRef {
			mode = 0755
		}
		fmt.Fprintf(&data, "%s\x00%s\x00%d\n", ref, hashes[ref], mode)
	}
	return digest([]byte(data.String()))
}

func (r *reader) payload(platform, binary string) string {
	docs := map[string]string{}
	for ref, b := range r.documents {
		docs[ref] = digest(b)
	}
	return PayloadSHA256(platform, binary, docs)
}

func localCommand(row map[string]any, sources gateSources) error {
	if row["actual_exit_code_observed"] != true || row["actual_exit_code"] != float64(0) || row["actual_signal"] != nil || row["timed_out"] != false || !absentFailure(row) {
		return reject("EVIDENCE", "local command failed, interrupted or unobserved")
	}
	if _, ok := row["actual_signal"]; !ok {
		return reject("EVIDENCE", "local command signal missing")
	}
	for _, key := range []string{"stdout_sha256", "stderr_sha256"} {
		if _, err := sources.dependency(row[key]); err != nil {
			return err
		}
	}
	return nil
}

func (r *reader) programGate(s gateSources, platform, binary string) error {
	o, err := parseObject(s.byKind["native-program-installation"])
	if err != nil {
		return err
	}
	payload := r.payload(platform, binary)
	if o["kind"] != "native-program-installation" || o["schema_version"] != float64(1) || o["fixture"] != false || o["status"] != "passed" || o["platform"] != platform || o["binary_sha256"] != binary || o["cli_commit"] != r.identity.CLICommit || o["cli_version"] != r.identity.CLIVersion || o["source_lock_sha256"] != r.identity.SourceLockSHA256 || o["package_payload_sha256"] != payload || o["input_drift"] != false || !empty(o["unexecuted"]) || !absentFailure(o) || !sameSet(stringsOf(o["required_cases"]), RequiredGateCoverage("program-installation")) {
		return reject("EVIDENCE", "invalid public program installation source")
	}
	archive := object(o["candidate_archive"])
	ref := FileRef{Path: text(archive["path"]), SHA256: text(archive["sha256"])}
	if _, err = s.dependency(ref.SHA256); err != nil {
		return err
	}
	raw, err := r.file(ref)
	if err != nil {
		return err
	}
	if err = r.candidateArchive(raw, platform, binary); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, value := range items(o["cases"]) {
		row := object(value)
		id := text(row["case"])
		if seen[id] || row["passed"] != true {
			return reject("EVIDENCE", "missing or duplicate installation assertion")
		}
		seen[id] = true
		switch id {
		case "install-plan-read-only":
			if row["whole_tree_unchanged"] != true {
				return reject("EVIDENCE", "installation preview wrote files")
			}
		case "install-apply":
			if row["installed_payload_sha256"] != payload {
				return reject("EVIDENCE", "installed payload mismatch")
			}
		case "rollback":
			if row["whole_inventory_restored"] != true {
				return reject("EVIDENCE", "program whole rollback incomplete")
			}
		case "recover":
			if row["repeated_recovery"] != true || row["no_overwrite"] != true {
				return reject("EVIDENCE", "program repeated recovery protection missing")
			}
		case "user-file-preservation":
			before := object(row["business_before"])
			if len(before) == 0 || !reflect.DeepEqual(before, object(row["business_after"])) {
				return reject("EVIDENCE", "program business inventory changed")
			}
			for _, descriptor := range before {
				if !fileDescriptor(descriptor, false) {
					return reject("EVIDENCE", "program business digest/mode missing")
				}
			}
		default:
			return reject("EVIDENCE", "unknown program installation case")
		}
	}
	if len(seen) != 5 {
		return reject("EVIDENCE", "program installation coverage incomplete")
	}
	commands := map[string]bool{}
	for _, v := range items(o["commands"]) {
		row := object(v)
		if err = localCommand(row, s); err != nil {
			return err
		}
		args := stringsOf(row["args"])
		if len(args) < 3 || args[1] != "update" {
			return reject("EVIDENCE", "program command is not the public update entry")
		}
		switch args[2] {
		case "plan", "apply", "rollback", "recover", "status":
			commands[args[2]] = true
		default:
			return reject("EVIDENCE", "unknown public update action")
		}
		stdout, _ := s.dependency(row["stdout_sha256"])
		if err = r.envelope(stdout, nil); err != nil {
			return err
		}
	}
	for _, action := range []string{"plan", "apply", "rollback", "recover"} {
		if !commands[action] {
			return reject("EVIDENCE", "public update %s was not executed", action)
		}
	}
	return nil
}

func (r *reader) candidateArchive(raw []byte, platform, binary string) error {
	files := map[string][]byte{}
	modes := map[string]uint32{}
	add := func(ref string, mode uint32, body []byte) error {
		if _, ok := files[ref]; ok {
			return reject("EVIDENCE", "duplicate candidate archive entry")
		}
		files[ref] = body
		modes[ref] = mode
		return nil
	}
	if strings.HasPrefix(platform, "windows/") {
		archive, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
		if err != nil {
			return reject("EVIDENCE", "invalid candidate zip")
		}
		for _, f := range archive.File {
			if !f.Mode().IsRegular() {
				return reject("EVIDENCE", "candidate archive contains non-file")
			}
			reader, err := f.Open()
			if err != nil {
				return err
			}
			body, err := io.ReadAll(io.LimitReader(reader, 256<<20))
			reader.Close()
			if err != nil {
				return err
			}
			if err = add(f.Name, uint32(f.Mode().Perm()), body); err != nil {
				return err
			}
		}
	} else {
		gzipReader, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return reject("EVIDENCE", "invalid candidate tar.gz")
		}
		defer gzipReader.Close()
		archive := tar.NewReader(gzipReader)
		for {
			h, err := archive.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return reject("EVIDENCE", "invalid candidate archive")
			}
			if h.Typeflag != tar.TypeReg {
				return reject("EVIDENCE", "candidate archive contains non-file")
			}
			body, err := io.ReadAll(io.LimitReader(archive, 256<<20))
			if err != nil {
				return err
			}
			if err = add(h.Name, uint32(h.Mode), body); err != nil {
				return err
			}
		}
	}
	binaryRef := "yss"
	if strings.HasPrefix(platform, "windows/") {
		binaryRef = "yss.exe"
	}
	if len(files) != len(r.documents)+2 || digest(files[binaryRef]) != binary || modes[binaryRef] != 0755 {
		return reject("EVIDENCE", "candidate binary/payload mismatch")
	}
	for ref, expected := range r.documents {
		if !bytes.Equal(files[ref], expected) || modes[ref] != 0644 {
			return reject("EVIDENCE", "candidate document mismatch %s", ref)
		}
	}
	manifest, err := parseObject(files["release-manifest.json"])
	if err != nil {
		return err
	}
	if modes["release-manifest.json"] != 0644 || manifest["schemaVersion"] != float64(1) || manifest["cliVersion"] != r.identity.CLIVersion || manifest["protocolVersion"] != float64(1) || manifest["cliCommit"] != r.identity.CLICommit || manifest["sourceState"] != "committed" || manifest["platform"] != platform || manifest["binarySha256"] != binary || manifest["stableReady"] != false || manifest["runtimeVerification"] != "passed" {
		return reject("PROVENANCE", "candidate manifest identity mismatch")
	}
	for _, key := range []string{"sourceLockSha256", "requiredPlatforms", "supportedPlatforms", "nativeReceiptSha256", "releaseGateSha256"} {
		if _, ok := manifest[key]; ok {
			return reject("EVIDENCE", "candidate cannot claim release proof")
		}
	}
	for _, p := range profiles {
		var expected any
		_ = json.Unmarshal(r.inspections[p], &expected)
		if !reflect.DeepEqual(object(manifest["bundles"])[p], expected) {
			return reject("PROVENANCE", "candidate complete inspection differs %s", p)
		}
	}
	return nil
}

var scopedConsumerCases = []string{"skills.projection", "skills.lock", "profiles.projection", "handoff.source-lock", "lifecycle.registry", "skills.registry", "affected-source-contract-tests", "source.whitespace", "skills.upstream-source"}

func scopedCommand(i int, args []string, cwd string) bool {
	sourceRoot := cwd + "/submodules/yss-harness-design-agent"
	if i == 8 {
		if len(args) != 3 {
			return false
		}
		// Imported Skill revisions are independently pinned and verified by the source script.
		sourceRoot = strings.TrimPrefix(args[2], "--source-root=")
		if !filepath.IsAbs(sourceRoot) || filepath.Clean(sourceRoot) != sourceRoot {
			return false
		}
	}
	commands := [][]string{{"scripts/sync-skills", "--check"}, {"scripts/update-skill-lock", "--check"}, {"scripts/sync-profile-skills", "--check", "--profile=all"}, {"scripts/verify-strategic-handoff-tools-lock", "--require-committed"}, {"scripts/verify-lifecycle-registry"}, {"scripts/verify-skill-registry"}, {"node", "--test", "--test-concurrency=1", "tests/read-only-intake.test.mjs", "tests/verification-execution.test.mjs", "tests/instance-metadata.test.mjs", ".template-source/tooling/node/test/retirement.test.mjs"}, {"git", "diff", "--check"}, {"scripts/verify-upstream-skill-source", "--source=iloveZzz/yss-harness-design-agent", "--source-root=" + sourceRoot}}
	return i >= 0 && i < len(commands) && reflect.DeepEqual(args, commands[i])
}
func (r *reader) scopedTemplateGate(s gateSources) error {
	o, err := parseObject(s.byKind["scoped-template-consumer-verification"])
	if err != nil {
		return err
	}
	if o["schema_version"] != float64(1) || o["kind"] != "scoped-template-consumer-verification" || o["qualification_scope"] != LocalQualificationScope || o["status"] != "passed" || o["input_drift"] != false || !empty(o["unexecuted"]) || !absentFailure(o) || o["source_state"] != "committed" || o["inputs_current_git_clean"] != true || o["reused"] != true {
		return reject("EVIDENCE", "invalid scoped template consumer report")
	}
	commits := object(o["current_sources"])
	if len(commits) != 4 {
		return reject("PROVENANCE", "scoped template sources incomplete")
	}
	for p, b := range r.identity.Bundles {
		if commits[p] != b.TemplateCommit {
			return reject("PROVENANCE", "scoped template source differs %s", p)
		}
	}
	if !sameSet(stringsOf(o["required_cases"]), scopedConsumerCases) {
		return reject("EVIDENCE", "scoped consumer coverage differs")
	}
	original := object(o["original_execution"])
	ref := FileRef{Path: text(original["path"]), SHA256: text(original["sha256"])}
	if _, err = s.dependency(ref.SHA256); err != nil {
		return err
	}
	raw, err := r.file(ref)
	if err != nil {
		return err
	}
	var actual []map[string]any
	if err = json.Unmarshal(raw, &actual); err != nil || len(actual) != 9 {
		return reject("EVIDENCE", "original source consumers incomplete")
	}
	rows := items(o["checks"])
	if len(rows) != 9 {
		return reject("EVIDENCE", "nine scoped checks required")
	}
	cwd := ""
	for i, v := range rows {
		row := object(v)
		origin := actual[i]
		if i == 0 {
			cwd = text(origin["cwd"])
		}
		seconds, ok := origin["seconds"].(float64)
		if row["id"] != scopedConsumerCases[i] || row["original_index"] != float64(i) || row["status"] != "passed" || row["actual_exit_code_observed"] != true || row["actual_exit_code"] != float64(0) || row["actual_signal"] != nil || !absentFailure(row) || origin["exit_code"] != float64(0) || origin["source_sha"] != r.identity.Bundles["spec"].TemplateCommit || cwd == "" || origin["cwd"] != cwd || !ok || seconds < 0 || !reflect.DeepEqual(row["args"], origin["command"]) || !scopedCommand(i, stringsOf(row["args"]), cwd) {
			return reject("EVIDENCE", "scoped original command identity or exit differs")
		}
		if _, ok = row["actual_signal"]; !ok {
			return reject("EVIDENCE", "scoped signal missing")
		}
		log := object(row["log"])
		logRef := FileRef{Path: text(log["path"]), SHA256: text(log["sha256"])}
		if !strings.HasSuffix(logRef.Path, "/"+text(origin["log"])) && logRef.Path != origin["log"] {
			return reject("EVIDENCE", "scoped original log path differs")
		}
		if _, err = s.dependency(logRef.SHA256); err != nil {
			return err
		}
		if _, err = r.file(logRef); err != nil {
			return err
		}
	}
	return nil
}
