package release

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"debug/buildinfo"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"
)

var Platforms = []string{"darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64", "windows/amd64", "windows/arm64"}
var Documents = []string{"docs/source-lock.json", "docs/compatibility.md", "docs/porting-status.md", "docs/native-governance.md", "docs/cli-retirement.md", "compat/README.md", "README.md"}
var nativeChecks = []string{"native-smoke", "native-recovery", "plugin-native-smoke"}
var releaseChecks = []string{"full-template-integration", "cli-integration", "legacy-recovery", "real-project-isolation"}
var shaPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var commitPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)
var stableVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

func reject(code, format string, args ...any) error {
	return &Error{Code: code, Detail: fmt.Sprintf(format, args...)}
}
func digest(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }
func decode(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return reject("SCHEMA", "%v", err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return reject("SCHEMA", "trailing JSON")
	}
	return nil
}

type guard struct {
	ref  string
	hash string
	mode os.FileMode
	size int64
}
type reader struct {
	root   string
	guards map[string]guard
}

func (r *reader) file(ref FileRef) ([]byte, error) {
	if ref.Path == "" || strings.Contains(ref.Path, "\\") || strings.Contains(ref.Path, ":") || path.IsAbs(ref.Path) || path.Clean(ref.Path) != ref.Path || ref.Path == "." || strings.HasPrefix(ref.Path, "../") || !shaPattern.MatchString(ref.SHA256) {
		return nil, reject("PATH", "invalid file descriptor %q", ref.Path)
	}
	p := r.root
	for _, part := range strings.Split(ref.Path, "/") {
		p = filepath.Join(p, part)
		info, err := os.Lstat(p)
		if err != nil {
			return nil, reject("PATH", "%s: %v", ref.Path, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, reject("PATH", "symlink %s", ref.Path)
		}
	}
	info, err := os.Lstat(p)
	if err != nil || !info.Mode().IsRegular() {
		return nil, reject("PATH", "not an ordinary file: %s", ref.Path)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, reject("PATH", "unreadable %s: %v", ref.Path, err)
	}
	if digest(b) != ref.SHA256 {
		return nil, reject("HASH", "digest mismatch %s", ref.Path)
	}
	g := guard{ref: ref.Path, hash: ref.SHA256, mode: info.Mode(), size: info.Size()}
	if old, ok := r.guards[ref.Path]; ok && old != g {
		return nil, reject("INPUT_DRIFT", "changed input %s", ref.Path)
	}
	r.guards[ref.Path] = g
	return b, nil
}
func (r *reader) unchanged() error {
	for _, g := range r.guards {
		info, err := os.Lstat(filepath.Join(r.root, filepath.FromSlash(g.ref)))
		if err != nil || info.Mode() != g.mode || info.Size() != g.size {
			return reject("INPUT_DRIFT", "changed input %s", g.ref)
		}
		if _, err = r.file(FileRef{Path: g.ref, SHA256: g.hash}); err != nil {
			return reject("INPUT_DRIFT", "%s: %v", g.ref, err)
		}
	}
	return nil
}
func validIdentity(got, expected Identity) error {
	if !stableVersion.MatchString(got.CLIVersion) || got.ProtocolVersion != 1 || !commitPattern.MatchString(got.CLICommit) || got.SourceState != "committed" || !shaPattern.MatchString(got.SourceLockSHA256) || len(got.Bundles) != 4 {
		return reject("PROVENANCE", "incomplete stable source identity")
	}
	for _, p := range []string{"spec", "design", "backend", "frontend"} {
		b, ok := got.Bundles[p]
		if !ok || !commitPattern.MatchString(b.TemplateCommit) || b.TemplateVersion != "git:"+b.TemplateCommit || b.SourceState != "committed" || !shaPattern.MatchString(b.SourceSnapshotHash) || !shaPattern.MatchString(b.ManifestHash) || !shaPattern.MatchString(b.BundleHash) {
			return reject("PROVENANCE", "invalid fixed bundle %s", p)
		}
	}
	if !reflect.DeepEqual(got, expected) {
		return reject("PROVENANCE", "input identity differs from frozen assembler source")
	}
	return nil
}
func passed(status string, exit *int, drift *bool, unexecuted []string) error {
	if status != "passed" || exit == nil || *exit != 0 || drift == nil || *drift || unexecuted == nil || len(unexecuted) != 0 {
		return reject("EVIDENCE", "failed, missing, drifting or unexecuted acceptance")
	}
	return nil
}
func (r *reader) checks(rows []Check, required []string, platform, binaryHash string) error {
	if len(rows) != len(required) {
		return reject("EVIDENCE", "required check set is incomplete")
	}
	seen := map[string]bool{}
	for _, row := range rows {
		allowed := false
		for _, id := range required {
			allowed = allowed || id == row.ID
		}
		if !allowed || seen[row.ID] {
			return reject("EVIDENCE", "duplicate or unknown check %q", row.ID)
		}
		seen[row.ID] = true
		if row.Signal != nil {
			return reject("EVIDENCE", "check was interrupted")
		}
		if err := passed(row.Status, row.ExitCode, row.InputDrift, row.Unexecuted); err != nil {
			return err
		}
		raw, err := r.file(row.Report)
		if err != nil {
			return err
		}
		for _, ref := range []FileRef{row.Stdout, row.Stderr} {
			if _, err = r.file(ref); err != nil {
				return err
			}
		}
		if platform != "" {
			if err = r.nativeReport(row.ID, row.Report.Path, raw, platform, binaryHash); err != nil {
				return err
			}
		} else {
			var obj map[string]any
			if err = json.Unmarshal(raw, &obj); err != nil || obj == nil {
				return reject("EVIDENCE", "invalid full-gate raw report %s", row.ID)
			}
			if err = rawExecution(obj); err != nil {
				return err
			}
		}
	}
	return nil
}

func rawExecution(obj map[string]any) error {
	observed := false
	if value, ok := obj["status"]; ok {
		observed = true
		if value != "passed" && value != "ok" {
			return reject("EVIDENCE", "raw gate did not pass")
		}
	}
	for _, key := range []string{"exitCode", "exit_code"} {
		if value, ok := obj[key]; ok {
			observed = true
			if value != float64(0) {
				return reject("EVIDENCE", "raw gate exit was not zero")
			}
		}
	}
	for _, key := range []string{"inputDrift", "input_drift"} {
		if value, ok := obj[key]; ok && value != false {
			return reject("EVIDENCE", "raw gate input drift")
		}
	}
	if value, ok := obj["unexecuted"]; ok {
		items, valid := value.([]any)
		if !valid || len(items) > 0 {
			return reject("EVIDENCE", "raw gate has unexecuted items")
		}
	}
	if !observed {
		return reject("EVIDENCE", "raw gate lacks observed success")
	}
	return nil
}
func (r *reader) envelope(raw []byte, identity *Identity) error {
	var env struct {
		OutputVersion   int    `json:"outputVersion"`
		ProtocolVersion int    `json:"protocolVersion"`
		Status          string `json:"status"`
		Code            string `json:"code"`
		Result          struct {
			Version         string `json:"version"`
			ProtocolVersion int    `json:"protocolVersion"`
			CLICommit       string `json:"cliCommit"`
			SourceState     string `json:"sourceState"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &env); err != nil || env.OutputVersion != 1 || env.ProtocolVersion != 1 || env.Status != "ok" || env.Code != "OK" {
		return reject("EVIDENCE", "invalid native version/envelope")
	}
	if identity != nil && (env.Result.Version != identity.CLIVersion || env.Result.ProtocolVersion != identity.ProtocolVersion || env.Result.CLICommit != identity.CLICommit || env.Result.SourceState != identity.SourceState) {
		return reject("PROVENANCE", "native version differs from fixed source")
	}
	return nil
}
func (r *reader) nativeReport(id, ref string, raw []byte, platform, binaryHash string) error {
	switch id {
	case "native-smoke":
		var report struct {
			Schema     int      `json:"schemaVersion"`
			Platform   string   `json:"platform"`
			Status     string   `json:"status"`
			Hash       string   `json:"binary_sha256"`
			Drift      *bool    `json:"input_drift"`
			Unexecuted []string `json:"unexecuted"`
			Results    []struct {
				Profile     string   `json:"profile"`
				Command     []string `json:"command"`
				Exit        *int     `json:"exitCode"`
				Status      string   `json:"status"`
				Code        string   `json:"code"`
				EnvelopeRef string   `json:"envelopeRef"`
				SHA256      string   `json:"sha256"`
			} `json:"results"`
		}
		if err := json.Unmarshal(raw, &report); err != nil || report.Schema != 1 || report.Platform != platform || report.Status != "passed" || report.Hash != binaryHash || report.Drift == nil || *report.Drift || report.Unexecuted == nil || len(report.Unexecuted) > 0 {
			return reject("EVIDENCE", "invalid native smoke report")
		}
		want := map[string]bool{":version": true, ":capabilities": true}
		for _, profile := range []string{"spec", "design", "backend", "frontend"} {
			for _, command := range []string{"init", "context verify", "diff", "doctor", "lifecycle query", "sync plan", "sync apply", "skills list", "assets list", "migrate status", "runtime begin", "runtime event", "runtime complete", "runtime run", "runtime events", "runtime commands", "runtime pin", "runtime pins", "runtime unpin", "runtime inspect"} {
				want[profile+":"+command] = true
			}
		}
		if len(report.Results) != len(want) {
			return reject("EVIDENCE", "incomplete native smoke surface: want %d commands", len(want))
		}
		seen := map[string]bool{}
		for _, row := range report.Results {
			if row.Exit == nil || *row.Exit != 0 || row.Status != "ok" || row.Code != "OK" || len(row.Command) == 0 {
				return reject("EVIDENCE", "failed native smoke command")
			}
			command := row.Command[0]
			if command == "sync" {
				for _, arg := range row.Command[1:] {
					if arg == "--plan" {
						command += " plan"
					}
					if arg == "--apply" {
						command += " apply"
					}
				}
			} else if command == "context" || command == "lifecycle" || command == "skills" || command == "assets" || command == "migrate" || command == "runtime" {
				if len(row.Command) < 2 {
					return reject("EVIDENCE", "missing native smoke subcommand")
				}
				command += " " + row.Command[1]
			}
			key := row.Profile + ":" + command
			if !want[key] || seen[key] {
				return reject("EVIDENCE", "duplicate or unknown native smoke command %s", key)
			}
			seen[key] = true
			if path.Base(row.EnvelopeRef) != row.EnvelopeRef || row.EnvelopeRef == "." {
				return reject("PATH", "invalid smoke envelope")
			}
			env, err := r.file(FileRef{Path: path.Join(path.Dir(ref), row.EnvelopeRef), SHA256: row.SHA256})
			if err != nil {
				return err
			}
			if err = r.envelope(env, nil); err != nil {
				return err
			}
		}
	case "native-recovery":
		var report struct {
			Schema     int      `json:"schema_version"`
			Kind       string   `json:"kind"`
			Platform   string   `json:"platform"`
			Status     string   `json:"status"`
			Hash       string   `json:"binary_sha256"`
			Drift      *bool    `json:"input_drift"`
			Unexecuted []string `json:"unexecuted"`
			Cases      []struct {
				Profile    string `json:"profile"`
				Kind       string `json:"case"`
				Status     string `json:"status"`
				Mutation   bool   `json:"observed_after_target_mutation"`
				Identity   bool   `json:"binding_and_identity_restored_together"`
				Repeat     bool   `json:"repeated_recovery"`
				ChildExit  *int   `json:"child_exit"`
				OutputHash string `json:"output_sha256"`
			} `json:"cases"`
		}
		if err := json.Unmarshal(raw, &report); err != nil || report.Schema != 1 || report.Kind != "native-process-recovery" || report.Platform != platform || report.Hash != binaryHash || report.Status != "passed" || report.Drift == nil || *report.Drift || report.Unexecuted == nil || len(report.Unexecuted) > 0 {
			return reject("EVIDENCE", "invalid native recovery report")
		}
		kinds := []string{"cancel", "terminate", "kill"}
		if strings.HasPrefix(platform, "windows/") {
			kinds = []string{"cancel", "kill"}
		}
		want := map[string]bool{}
		for _, p := range []string{"spec", "design", "backend", "frontend"} {
			for _, kind := range kinds {
				want[p+"-"+kind] = true
			}
		}
		if len(report.Cases) != len(want) {
			return reject("EVIDENCE", "missing native recovery cases")
		}
		seen := map[string]bool{}
		for _, row := range report.Cases {
			label := row.Profile + "-" + row.Kind
			if !want[label] || seen[label] || row.Status != "passed" || !row.Mutation || !row.Identity || !row.Repeat || row.ChildExit == nil || *row.ChildExit == 0 {
				return reject("EVIDENCE", "invalid native recovery case %s", label)
			}
			seen[label] = true
			if _, err := r.file(FileRef{Path: path.Join(path.Dir(ref), "output-"+label+"-apply.json"), SHA256: row.OutputHash}); err != nil {
				return err
			}
		}
	case "plugin-native-smoke":
		var report struct {
			Schema   int    `json:"schemaVersion"`
			Kind     string `json:"kind"`
			Status   string `json:"status"`
			Platform struct {
				OS   string `json:"os"`
				Arch string `json:"arch"`
			} `json:"platform"`
			Hash        string `json:"binary_sha256"`
			AfterHash   string `json:"binary_after_sha256"`
			Drift       *bool  `json:"input_drift"`
			BinaryDrift *bool  `json:"binary_drift"`
			Cases       []struct {
				Profile string `json:"profile"`
				Status  string `json:"status"`
			} `json:"cases"`
			Commands []struct {
				Observed bool `json:"exit_observed"`
				Exit     *int `json:"exit_code"`
				Signal   any  `json:"signal"`
				Error    any  `json:"error"`
				Stdout   struct {
					File   string `json:"file"`
					SHA256 string `json:"sha256"`
				} `json:"stdout"`
				Stderr struct {
					File   string `json:"file"`
					SHA256 string `json:"sha256"`
				} `json:"stderr"`
			} `json:"commands"`
		}
		if err := json.Unmarshal(raw, &report); err != nil || report.Schema != 1 || report.Kind != "native-plugin-public-smoke" || report.Status != "passed" || report.Hash != binaryHash || report.AfterHash != binaryHash || report.Drift == nil || *report.Drift || report.BinaryDrift == nil || *report.BinaryDrift {
			return reject("EVIDENCE", "invalid native plugin report")
		}
		osname := report.Platform.OS
		if osname == "win32" {
			osname = "windows"
		}
		arch := report.Platform.Arch
		if arch == "x64" {
			arch = "amd64"
		}
		if osname+"/"+arch != platform {
			return reject("EVIDENCE", "plugin runner platform mismatch")
		}
		seen := map[string]bool{}
		for _, row := range report.Cases {
			if (row.Profile != "spec" && row.Profile != "design") || seen[row.Profile] || row.Status != "passed" {
				return reject("EVIDENCE", "invalid plugin case")
			}
			seen[row.Profile] = true
		}
		if len(seen) != 2 || len(report.Commands) == 0 {
			return reject("EVIDENCE", "incomplete native plugin acceptance")
		}
		for _, row := range report.Commands {
			if !row.Observed || row.Exit == nil || *row.Exit != 0 || row.Signal != nil || row.Error != nil {
				return reject("EVIDENCE", "failed plugin command")
			}
			for _, log := range []struct {
				File   string `json:"file"`
				SHA256 string `json:"sha256"`
			}{row.Stdout, row.Stderr} {
				if path.Base(log.File) != log.File || log.File == "." {
					return reject("PATH", "invalid plugin log")
				}
				if _, err := r.file(FileRef{Path: path.Join(path.Dir(ref), log.File), SHA256: log.SHA256}); err != nil {
					return err
				}
			}
		}
	default:
		return reject("EVIDENCE", "unknown native check")
	}
	return nil
}
func binaryIdentity(raw []byte, platform string, identity Identity) error {
	info, err := buildinfo.Read(bytes.NewReader(raw))
	if err != nil {
		return reject("BINARY", "not a Go executable: %v", err)
	}
	settings := map[string]string{}
	for _, s := range info.Settings {
		settings[s.Key] = s.Value
	}
	parts := strings.Split(platform, "/")
	if info.Path != "github.com/iloveZzz/yss-cli/cmd/yss" || info.Main.Path != "github.com/iloveZzz/yss-cli" || settings["GOOS"] != parts[0] || settings["GOARCH"] != parts[1] || settings["CGO_ENABLED"] != "0" {
		return reject("BINARY", "binary module/platform/CGO mismatch %s", platform)
	}
	flags := strings.Fields(settings["-ldflags"])
	values := map[string]string{}
	stripS, stripW := false, false
	for i, f := range flags {
		stripS = stripS || f == "-s"
		stripW = stripW || f == "-w"
		if f == "-X" && i+1 < len(flags) {
			pair := strings.SplitN(flags[i+1], "=", 2)
			if len(pair) == 2 {
				if _, ok := values[pair[0]]; ok {
					return reject("PROVENANCE", "duplicate build source override")
				}
				values[pair[0]] = pair[1]
			}
		}
	}
	// Go omits -ldflags from build info with -trimpath. The same-binary native
	// version envelope binds the injected identity; available VCS/flags may never
	// contradict it. Strip/platform checks use the actual executable headers.
	if (settings["-ldflags"] != "" && (!stripS || !stripW || values["github.com/iloveZzz/yss-cli/internal/domain.BuildCommit"] != identity.CLICommit || values["github.com/iloveZzz/yss-cli/internal/domain.BuildSourceState"] != "committed")) || settings["vcs.modified"] == "true" || (settings["vcs.revision"] != "" && settings["vcs.revision"] != identity.CLICommit) {
		return reject("PROVENANCE", "binary is not stripped/fixed/committed")
	}
	return strippedExecutable(raw, platform)
}

func strippedExecutable(raw []byte, platform string) error {
	parts := strings.Split(platform, "/")
	arch := parts[1]
	switch parts[0] {
	case "linux":
		f, err := elf.NewFile(bytes.NewReader(raw))
		if err != nil {
			return reject("BINARY", "invalid ELF")
		}
		defer f.Close()
		machine := elf.EM_X86_64
		if arch == "arm64" {
			machine = elf.EM_AARCH64
		}
		if f.Machine != machine || f.Class != elf.ELFCLASS64 || f.Section(".symtab") != nil {
			return reject("BINARY", "wrong ELF architecture or unstripped symbol table")
		}
		for _, s := range f.Sections {
			if strings.HasPrefix(s.Name, ".debug") || strings.HasPrefix(s.Name, ".zdebug") {
				return reject("BINARY", "unstripped ELF debug sections")
			}
		}
	case "darwin":
		f, err := macho.NewFile(bytes.NewReader(raw))
		if err != nil {
			return reject("BINARY", "invalid Mach-O")
		}
		defer f.Close()
		cpu := macho.CpuAmd64
		if arch == "arm64" {
			cpu = macho.CpuArm64
		}
		if f.Cpu != cpu || f.Type != macho.TypeExec || (f.Dysymtab != nil && f.Dysymtab.Nlocalsym != 0) {
			return reject("BINARY", "wrong Mach-O architecture or local symbols")
		}
		for _, s := range f.Sections {
			if s.Seg == "__DWARF" || strings.HasPrefix(s.Name, "__debug") {
				return reject("BINARY", "unstripped Mach-O debug sections")
			}
		}
	case "windows":
		f, err := pe.NewFile(bytes.NewReader(raw))
		if err != nil {
			return reject("BINARY", "invalid PE")
		}
		defer f.Close()
		machine := uint16(pe.IMAGE_FILE_MACHINE_AMD64)
		if arch == "arm64" {
			machine = pe.IMAGE_FILE_MACHINE_ARM64
		}
		if f.Machine != machine || len(f.COFFSymbols) != 0 {
			return reject("BINARY", "wrong PE architecture or unstripped symbol table")
		}
		for _, s := range f.Sections {
			if strings.HasPrefix(s.Name, ".debug") {
				return reject("BINARY", "unstripped PE debug sections")
			}
		}
	}
	return nil
}

// Assemble validates every named input before creating output, then rechecks it
// before publishing the new directory. Receipt assertions are supplied by the
// release verifier; this tool never creates runtime evidence or grants approval.
func Assemble(manifestPath, out string, expected Expected) error {
	manifestPath, err := filepath.Abs(manifestPath)
	if err != nil {
		return err
	}
	base, err := filepath.EvalSymlinks(filepath.Dir(manifestPath))
	if err != nil {
		return reject("PATH", "manifest parent: %v", err)
	}
	manifestPath = filepath.Join(base, filepath.Base(manifestPath))
	info, err := os.Lstat(manifestPath)
	if err != nil || !info.Mode().IsRegular() {
		return reject("PATH", "manifest must be an ordinary file")
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	r := &reader{root: base, guards: map[string]guard{}}
	if _, err = r.file(FileRef{Path: filepath.Base(manifestPath), SHA256: digest(raw)}); err != nil {
		return err
	}
	var input Input
	if err = decode(raw, &input); err != nil {
		return err
	}
	if len(input.Artifacts) != 6 {
		return reject("PLATFORMS", "six native artifacts required")
	}
	if input.SchemaVersion != 1 {
		return reject("SCHEMA", "unsupported input schema")
	}
	if err = validIdentity(input.Identity, expected.Identity); err != nil {
		return err
	}
	platforms := map[string]Artifact{}
	for _, a := range input.Artifacts {
		known := false
		for _, p := range Platforms {
			known = known || a.Platform == p
		}
		if !known {
			return reject("PLATFORMS", "unsupported platform %s", a.Platform)
		}
		if _, ok := platforms[a.Platform]; ok {
			return reject("PLATFORMS", "duplicate platform %s", a.Platform)
		}
		platforms[a.Platform] = a
	}
	if len(input.Documents) != len(Documents) || len(expected.Documents) != len(Documents) {
		return reject("PROVENANCE", "fixed document set required")
	}
	docs := map[string][]byte{}
	for _, ref := range Documents {
		b, err := r.file(input.Documents[ref])
		if err != nil {
			return err
		}
		if !bytes.Equal(b, expected.Documents[ref]) {
			return reject("PROVENANCE", "document differs from frozen source %s", ref)
		}
		docs[ref] = b
	}
	if digest(docs["docs/source-lock.json"]) != input.SourceLockSHA256 {
		return reject("PROVENANCE", "source-lock digest mismatch")
	}
	var lock struct {
		Schema   int `json:"schemaVersion"`
		Profiles map[string]struct {
			Commit string `json:"templateCommit"`
		} `json:"profiles"`
	}
	if err = json.Unmarshal(docs["docs/source-lock.json"], &lock); err != nil || lock.Schema != 2 || len(lock.Profiles) != 4 {
		return reject("PROVENANCE", "invalid fixed source lock")
	}
	for p, b := range input.Bundles {
		if lock.Profiles[p].Commit != b.TemplateCommit {
			return reject("PROVENANCE", "source-lock template mismatch %s", p)
		}
	}
	raw, err = r.file(input.ReleaseGate)
	if err != nil {
		return err
	}
	var gate Receipt
	if err = decode(raw, &gate); err != nil {
		return err
	}
	if gate.SchemaVersion != 1 {
		return reject("SCHEMA", "invalid release gate schema")
	}
	if err = validIdentity(gate.Identity, input.Identity); err != nil {
		return err
	}
	if err = passed(gate.Status, gate.ExitCode, gate.InputDrift, gate.Unexecuted); err != nil {
		return err
	}
	if err = r.checks(gate.Checks, releaseChecks, "", ""); err != nil {
		return err
	}
	contents := map[string][]byte{}
	for _, p := range Platforms {
		a := platforms[p]
		bin, err := r.file(a.Binary)
		if err != nil {
			return err
		}
		if err = binaryIdentity(bin, p, input.Identity); err != nil {
			return err
		}
		raw, err = r.file(a.Receipt)
		if err != nil {
			return err
		}
		var receipt Receipt
		if err = decode(raw, &receipt); err != nil {
			return err
		}
		if receipt.SchemaVersion != 1 || receipt.Platform != p || receipt.RuntimePlatform != p || receipt.RuntimeVerification != "native-runner" || receipt.BinarySHA256 != a.Binary.SHA256 || receipt.BinaryBeforeSHA256 != a.Binary.SHA256 || receipt.BinaryAfterSHA256 != a.Binary.SHA256 {
			return reject("EVIDENCE", "native runner/binary mismatch %s", p)
		}
		if err = validIdentity(receipt.Identity, input.Identity); err != nil {
			return err
		}
		if err = passed(receipt.Status, receipt.ExitCode, receipt.InputDrift, receipt.Unexecuted); err != nil {
			return err
		}
		if receipt.Error != "" {
			return reject("EVIDENCE", "native receipt has an error")
		}
		version, err := r.file(receipt.Version)
		if err != nil {
			return err
		}
		if err = r.envelope(version, &input.Identity); err != nil {
			return err
		}
		if err = r.additionalCommands(receipt, version, input.Identity); err != nil {
			return err
		}
		if err = r.checks(receipt.Checks, nativeChecks, p, a.Binary.SHA256); err != nil {
			return err
		}
		contents[p] = bin
	}
	out, err = filepath.Abs(out)
	if err != nil {
		return err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(out))
	if err != nil {
		return reject("PATH", "output parent must exist: %v", err)
	}
	out = filepath.Join(parent, filepath.Base(out))
	if expected.RepositoryRoot != "" {
		root, err := filepath.EvalSymlinks(expected.RepositoryRoot)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, out)
		if err != nil || rel == "." || !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return reject("PATH", "output must be outside repository")
		}
	}
	if _, err = os.Lstat(out); !os.IsNotExist(err) {
		return reject("EXISTS", "output must be new")
	}
	if err = r.unchanged(); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(parent, ".yss-release-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	records := []map[string]any{}
	for _, p := range Platforms {
		parts := strings.Split(p, "/")
		name := "yss"
		ext := ".tar.gz"
		if parts[0] == "windows" {
			name = "yss.exe"
			ext = ".zip"
		}
		files := map[string][]byte{}
		for ref, b := range docs {
			files[ref] = b
		}
		files[name] = contents[p]
		descriptors := map[string]map[string]any{}
		for ref, b := range files {
			mode := uint32(0644)
			if ref == name {
				mode = 0755
			}
			descriptors[ref] = map[string]any{"type": "file", "digest": digest(b), "mode": mode}
		}
		manifest := map[string]any{"schemaVersion": 1, "cliVersion": input.CLIVersion, "protocolVersion": input.ProtocolVersion, "cliCommit": input.CLICommit, "sourceState": "committed", "bundles": input.Bundles, "sourceLockSha256": input.SourceLockSHA256, "platform": p, "cgo": false, "binarySha256": digest(contents[p]), "stableReady": true, "runtimeVerification": "passed", "nativeReceiptSha256": platforms[p].Receipt.SHA256, "releaseGateSha256": input.ReleaseGate.SHA256, "files": descriptors}
		b, err := json.MarshalIndent(manifest, "", "  ")
		if err != nil {
			return err
		}
		files["release-manifest.json"] = append(b, '\n')
		archive := "yss_" + input.CLIVersion + "_" + parts[0] + "_" + parts[1] + ext
		if err = writeArchive(filepath.Join(stage, archive), files, name, ext == ".zip"); err != nil {
			return err
		}
		b, err = os.ReadFile(filepath.Join(stage, archive))
		if err != nil {
			return err
		}
		records = append(records, map[string]any{"platform": p, "archive": archive, "sha256": digest(b), "bytes": len(b), "compiled": false, "nativeRuntimeVerified": true, "binarySha256": digest(contents[p]), "nativeReceiptSha256": platforms[p].Receipt.SHA256})
	}
	proof := map[string]any{"schemaVersion": 2, "version": input.CLIVersion, "cliCommit": input.CLICommit, "sourceState": "committed", "bundles": input.Bundles, "sourceLockSha256": input.SourceLockSHA256, "stableReady": true, "pending": []string{}, "artifacts": records, "inputManifestSha256": r.guards[filepath.Base(manifestPath)].hash, "releaseGateSha256": input.ReleaseGate.SHA256}
	b, err := json.MarshalIndent(proof, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(stage, "checksums.json"), append(b, '\n'), 0644); err != nil {
		return err
	}
	if err = r.unchanged(); err != nil {
		return err
	}
	if _, err = os.Lstat(out); !os.IsNotExist(err) {
		return reject("EXISTS", "output appeared while assembling")
	}
	// Claim the new name atomically and create each file exclusively. A failed
	// publication preserves partial evidence without replacing any existing file;
	// checksums.json is written last, after a second complete input guard.
	if err = os.Mkdir(out, 0755); err != nil {
		return reject("EXISTS", "cannot claim new output: %v", err)
	}
	for _, record := range records {
		ref := record["archive"].(string)
		raw, err := os.ReadFile(filepath.Join(stage, ref))
		if err != nil {
			return err
		}
		if err = exclusiveFile(filepath.Join(out, ref), raw); err != nil {
			return err
		}
	}
	if err = r.unchanged(); err != nil {
		return err
	}
	return exclusiveFile(filepath.Join(out, "checksums.json"), append(b, '\n'))
}

func (r *reader) additionalCommands(receipt Receipt, version []byte, identity Identity) error {
	check := func(row Check) error {
		if row.Signal != nil {
			return reject("EVIDENCE", "native source command was interrupted")
		}
		if err := passed(row.Status, row.ExitCode, row.InputDrift, row.Unexecuted); err != nil {
			return err
		}
		for _, ref := range []FileRef{row.Stdout, row.Stderr} {
			if _, err := r.file(ref); err != nil {
				return err
			}
		}
		return nil
	}
	if row := receipt.VersionCommand; row != nil {
		if row.ID != "native-version" {
			return reject("EVIDENCE", "wrong version command identity")
		}
		if err := check(*row); err != nil {
			return err
		}
		raw, err := r.file(row.Stdout)
		if err != nil {
			return err
		}
		if !bytes.Equal(raw, version) {
			return reject("EVIDENCE", "version command stdout differs from envelope")
		}
	}
	if receipt.BundleCommands != nil {
		if len(receipt.BundleCommands) != 4 {
			return reject("EVIDENCE", "incomplete native inspect commands")
		}
		seen := map[string]bool{}
		for _, row := range receipt.BundleCommands {
			if err := check(row); err != nil {
				return err
			}
			profile := strings.TrimPrefix(row.ID, "native-inspect-")
			want, ok := identity.Bundles[profile]
			if !ok || seen[profile] {
				return reject("EVIDENCE", "unknown or duplicate native inspect command")
			}
			seen[profile] = true
			raw, err := r.file(row.Report)
			if err != nil {
				return err
			}
			if err = r.envelope(raw, nil); err != nil {
				return err
			}
			var env struct {
				Result BundleIdentity `json:"result"`
			}
			if err = json.Unmarshal(raw, &env); err != nil || !reflect.DeepEqual(env.Result, want) {
				return reject("PROVENANCE", "native inspect differs from fixed bundle")
			}
			stdout, err := r.file(row.Stdout)
			if err != nil {
				return err
			}
			if !bytes.Equal(stdout, raw) {
				return reject("EVIDENCE", "inspect stdout differs from envelope")
			}
		}
	}
	return nil
}

func exclusiveFile(ref string, raw []byte) error {
	f, err := os.OpenFile(ref, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return reject("EXISTS", "cannot create output %s: %v", filepath.Base(ref), err)
	}
	defer f.Close()
	if _, err = f.Write(raw); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	return f.Close()
}
func writeArchive(file string, files map[string][]byte, bin string, zipFormat bool) error {
	f, err := os.Create(file)
	if err != nil {
		return err
	}
	defer f.Close()
	refs := make([]string, 0, len(files))
	for ref := range files {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	if zipFormat {
		w := zip.NewWriter(f)
		for _, ref := range refs {
			h := &zip.FileHeader{Name: ref, Method: zip.Deflate}
			h.SetModTime(time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC))
			h.SetMode(0644)
			if ref == bin {
				h.SetMode(0755)
			}
			d, err := w.CreateHeader(h)
			if err != nil {
				return err
			}
			if _, err = d.Write(files[ref]); err != nil {
				return err
			}
		}
		if err = w.Close(); err != nil {
			return err
		}
	} else {
		g := gzip.NewWriter(f)
		w := tar.NewWriter(g)
		for _, ref := range refs {
			mode := int64(0644)
			if ref == bin {
				mode = 0755
			}
			if err = w.WriteHeader(&tar.Header{Name: ref, Mode: mode, Size: int64(len(files[ref])), ModTime: time.Unix(0, 0)}); err != nil {
				return err
			}
			if _, err = w.Write(files[ref]); err != nil {
				return err
			}
		}
		if err = w.Close(); err != nil {
			return err
		}
		if err = g.Close(); err != nil {
			return err
		}
	}
	if err = f.Sync(); err != nil {
		return err
	}
	return f.Close()
}
