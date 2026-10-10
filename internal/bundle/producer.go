package bundle

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

// Transitional declaration policies, never legacy executable code or file snapshots.
//
//go:embed rules/*.json
var rules embed.FS

type Provenance struct {
	Version     string `json:"version"`
	Commit      string `json:"commit,omitempty"`
	SourceState string `json:"sourceState"`
}
type LegacyLineage struct {
	Version      string `json:"version,omitempty"`
	CLICommit    string `json:"cliCommit,omitempty"`
	SnapshotHash string `json:"sourceSnapshotHash,omitempty"`
	ManifestHash string `json:"manifestHash,omitempty"`
}
type PolicyProvenance struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
	Kind   string `json:"kind"`
}
type Source struct {
	SkillsSource    *SkillSource  `json:"skillsSource,omitempty"`
	Profile         string        `json:"profile"`
	Root            string        `json:"-"`
	SourcePath      string        `json:"sourcePath"`
	Commit          string        `json:"templateCommit"`
	TemplateVersion string        `json:"templateVersion"`
	PolicyPath      string        `json:"policyPath"`
	PolicyHash      string        `json:"policyHash"`
	Legacy          LegacyLineage `json:"legacy"`
	Producer        Provenance    `json:"-"`
}
type SourceLock struct {
	SchemaVersion int               `json:"schemaVersion"`
	Producer      Provenance        `json:"producer"`
	Profiles      map[string]Source `json:"profiles"`
}
type Entries struct {
	Files    []string `json:"files,omitempty"`
	Scripts  []string `json:"scripts,omitempty"`
	Prefixes []string `json:"prefixes,omitempty"`
	Skills   []string `json:"skills,omitempty"`
}
type Policy struct {
	Upgrade       *UpgradePolicy     `json:"upgradePolicy,omitempty"`
	SchemaVersion int                `json:"schemaVersion"`
	Profile       string             `json:"profile"`
	Manifest      map[string]any     `json:"manifest"`
	Common        Entries            `json:"common,omitempty"`
	Stages        map[string]Entries `json:"stages,omitempty"`
	OmitPaths     []string           `json:"omitPaths,omitempty"`
}
type sourceFile struct {
	data []byte
	mode uint32
}

var fullCommit = regexp.MustCompile(`^[a-f0-9]{40}$`)

// Build reads only the named Git object tree. Dirty checkout bytes and legacy CLI
// directories cannot influence it. The policy digest is a required source lock.
func Build(ctx context.Context, s Source) (*Bundle, error) {
	if _, e := domain.GetProfile(s.Profile); e != nil {
		return nil, e
	}
	if !fullCommit.MatchString(s.Commit) {
		return nil, fmt.Errorf("templateCommit requires a full 40 character Git SHA")
	}
	if s.PolicyHash == "" {
		return nil, fmt.Errorf("policyHash is required")
	}
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	root, e := filepath.Abs(s.Root)
	if e != nil {
		return nil, e
	}
	kindBytes, err := git(ctx, root, "cat-file", "-t", s.Commit)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(kindBytes)) != "commit" {
		return nil, fmt.Errorf("templateCommit must name a Git commit")
	}
	if s.PolicyPath == "" {
		s.PolicyPath = ".template-source/distribution/bundle-profile.json"
	}
	var policyBytes []byte
	kind := "committed"
	if strings.HasPrefix(s.PolicyPath, "builtin:") {
		if s.PolicyPath != "builtin:"+s.Profile {
			return nil, fmt.Errorf("policy profile mismatch")
		}
		policyBytes, e = rules.ReadFile("rules/" + s.Profile + ".json")
		kind = "bootstrap"
	} else {
		if e = safefs.ValidateRef(s.PolicyPath); e != nil {
			return nil, e
		}
		entry, err := git(ctx, root, "ls-tree", s.Commit, "--", s.PolicyPath)
		if err != nil {
			return nil, err
		}
		fields := strings.Fields(string(entry))
		if len(fields) < 3 || fields[0] != "100644" || fields[1] != "blob" {
			return nil, fmt.Errorf("policy must be a committed regular JSON file")
		}
		policyBytes, e = git(ctx, root, "show", s.Commit+":"+s.PolicyPath)
	}
	if e != nil {
		return nil, e
	}
	if safefs.Digest(policyBytes) != s.PolicyHash {
		return nil, fmt.Errorf("policyHash drift: %s", s.Profile)
	}
	var policy Policy
	if e = json.Unmarshal(policyBytes, &policy); e != nil {
		return nil, e
	}
	if policy.SchemaVersion != 1 || policy.Profile != s.Profile || policy.Manifest == nil {
		return nil, fmt.Errorf("invalid bundle policy")
	}
	if e = validatePolicy(policy); e != nil {
		return nil, e
	}
	raw, e := readGitFiles(ctx, root, s.Commit, policy.Manifest)
	if e != nil {
		return nil, e
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("empty template source")
	}
	if s.SkillsSource != nil {
		provenance, err := composeCommittedSkills(ctx, s, raw)
		if err != nil {
			return nil, err
		}
		policy.Manifest["skillComposition"] = provenance
	}
	if s.TemplateVersion == "" {
		s.TemplateVersion = "git:" + s.Commit
	}
	if s.Producer.Version == "" {
		s.Producer = Provenance{Version: "bundle-producer-v3", SourceState: "unspecified"}
	}
	manifestBytes, _ := json.Marshal(policy.Manifest)
	b := &Bundle{SchemaVersion: 3, Upgrade: policy.Upgrade, Profile: s.Profile, TemplateVersion: s.TemplateVersion, LegacyVersion: s.Legacy.Version, LegacyCLICommit: s.Legacy.CLICommit, Legacy: s.Legacy, TemplateCommit: s.Commit, SourceState: "committed", ManifestHash: safefs.Digest(manifestBytes), Manifest: policy.Manifest, SourcePolicy: PolicyProvenance{Path: s.PolicyPath, Digest: s.PolicyHash, Kind: kind}, Producer: s.Producer, Files: map[string]File{}, Initial: map[string]File{}}
	if b.Upgrade == nil {
		b.Upgrade = DefaultUpgradePolicy()
	}
	snapshot := map[string]File{}
	for ref, f := range raw {
		snapshot[ref] = encodedFile(f.data, f.mode, "")
	}
	encoded, _ := json.Marshal(snapshot)
	b.SnapshotHash = safefs.Digest(encoded)
	transforms, e := nativeUpgradeSource(raw)
	if e != nil {
		return nil, e
	}
	layoutTransforms, e := nativeWorkLayoutSource(raw)
	if e != nil {
		return nil, e
	}
	transforms = append(transforms, layoutTransforms...)
	renderSet := stringSet(stringsFrom(policy.Manifest["renderPaths"]))
	for ref, f := range raw {
		if !includedByManifest(ref, policy.Manifest, true) {
			continue
		}
		data := prepareSource(s.Profile, ref, f.data)
		if renderSet[ref] {
			// Full spec still targets a project-instance with selected runtimes.
			// Preserve all assets; only the lock stays complete so the consumer
			// can derive its exact installed Skill/runtime selection dynamically.
			selectedInstance := s.Profile == "spec" && ref != "skills-lock.json"
			data, e = renderSource(s.Profile, ref, data, selectedInstance, nil)
			if e != nil {
				return nil, e
			}
		}
		b.Files[ref] = encodedFile(nativeGuidance(s.Profile, ref, data), f.mode, ownership(s.Profile, ref, policy.Manifest))
	}
	for _, t := range transforms {
		if f, included := b.Files[t.Path]; included {
			t.OutputDigest = f.Digest
			b.NativeTransforms = append(b.NativeTransforms, t)
		}
	}
	if e = bindBuiltSkillLock(b.Files); e != nil {
		return nil, e
	}
	if s.Profile != "spec" {
		b.Distribution = map[string]any{"mode": "profile-full"}
		for ref, f := range b.Files {
			b.Initial[ref] = f
		}
	} else {
		if e = buildSelective(b, raw, policy, renderSet); e != nil {
			return nil, e
		}
		if f, exists := b.Files["skills-lock.json"]; exists {
			data, err := base64.StdEncoding.DecodeString(f.Data)
			if err != nil {
				return nil, err
			}
			data, err = SelectedSourceLock(data, stringsFrom(b.Distribution["installedSkills"]), stringsFrom(b.Distribution["runtimes"]))
			if err != nil {
				return nil, err
			}
			b.Initial["skills-lock.json"] = encodedFile(data, f.Mode, f.Ownership)
		}
		if e = bindBuiltSkillLock(b.Initial); e != nil {
			return nil, e
		}
	}
	if e = validate(b, s.Profile); e != nil {
		return nil, e
	}
	b.BundleHash = contentHash(b)
	return b, nil
}
func BuildLock(ctx context.Context, lock SourceLock) (map[string]*Bundle, error) {
	if (lock.SchemaVersion != 2 && lock.SchemaVersion != 3) || len(lock.Profiles) != 4 {
		return nil, fmt.Errorf("source lock v2/v3 requires four profiles")
	}
	out := map[string]*Bundle{}
	for _, profile := range []string{"spec", "design", "backend", "frontend"} {
		s, ok := lock.Profiles[profile]
		if !ok || s.Profile != profile {
			return nil, fmt.Errorf("source lock profile missing: %s", profile)
		}
		if lock.SchemaVersion == 2 && s.SkillsSource != nil {
			return nil, fmt.Errorf("skill composition requires source lock v3")
		}
		if lock.SchemaVersion == 3 && (profile == "backend" || profile == "frontend") && s.SkillsSource == nil {
			return nil, fmt.Errorf("%s: source lock v3 requires skillsSource", profile)
		}
		s.Producer = lock.Producer
		b, e := Build(ctx, s)
		if e != nil {
			return nil, fmt.Errorf("%s: %w", profile, e)
		}
		out[profile] = b
	}
	return out, nil
}

// WriteBuilt verifies and atomically replaces one deterministic shared archive.
func WriteBuilt(out string, bundles map[string]*Bundle) (*StorageReport, error) {
	data, report, e := packBundles(bundles)
	if e != nil {
		return nil, e
	}
	if _, e := safefs.Path(out, "probe"); e != nil {
		return nil, e
	}
	if e := os.MkdirAll(out, 0755); e != nil {
		return nil, e
	}
	file, e := safefs.Path(out, report.Filename)
	if e != nil {
		return nil, e
	}
	temp, e := os.CreateTemp(out, ".bundle-")
	if e != nil {
		return nil, e
	}
	name := temp.Name()
	defer os.Remove(name)
	if _, e = temp.Write(data); e == nil {
		e = temp.Chmod(0644)
	}
	if ce := temp.Close(); e == nil {
		e = ce
	}
	if e == nil {
		e = os.Rename(name, file)
	}
	if e != nil {
		return nil, e
	}
	return report, nil
}
func git(ctx context.Context, root string, args ...string) ([]byte, error) {
	c := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	var errout bytes.Buffer
	c.Stderr = &errout
	o, e := c.Output()
	if e != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), e, errout.String())
	}
	return o, nil
}
func readGitFiles(ctx context.Context, root, commit string, manifest map[string]any) (map[string]sourceFile, error) {
	listing, e := git(ctx, root, "ls-tree", "-rz", "--full-tree", commit)
	if e != nil {
		return nil, e
	}
	type obj struct {
		ref, id string
		mode    uint32
	}
	objects := []obj{}
	for _, record := range bytes.Split(listing, []byte{0}) {
		if len(record) == 0 {
			continue
		}
		pair := bytes.SplitN(record, []byte{'\t'}, 2)
		if len(pair) != 2 {
			return nil, fmt.Errorf("invalid git tree")
		}
		header := strings.Fields(string(pair[0]))
		ref := string(pair[1])
		if !includedByManifest(ref, manifest, false) {
			continue
		}
		if e = safefs.ValidateRef(ref); e != nil {
			return nil, e
		}
		if len(header) != 3 || header[1] != "blob" || (header[0] != "100644" && header[0] != "100755" && !(header[0] == "120000" && projectionLink(ref))) {
			return nil, fmt.Errorf("unsupported source entry: %s", ref)
		}
		mode := uint32(0644)
		if header[0] == "120000" {
			mode = 0
		}
		if header[0] == "100755" {
			mode = 0755
		}
		objects = append(objects, obj{ref, header[2], mode})
	}
	var input strings.Builder
	for _, o := range objects {
		input.WriteString(o.id)
		input.WriteByte('\n')
	}
	cmd := exec.CommandContext(ctx, "git", "-C", root, "cat-file", "--batch")
	cmd.Stdin = strings.NewReader(input.String())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, e := cmd.StdoutPipe()
	if e != nil {
		return nil, e
	}
	if e = cmd.Start(); e != nil {
		return nil, e
	}
	r := bufio.NewReader(stdout)
	out := map[string]sourceFile{}
	var readErr error
	var total int64
	for _, o := range objects {
		line, e := r.ReadString('\n')
		if e != nil {
			readErr = e
			break
		}
		h := strings.Fields(line)
		if len(h) != 3 || h[0] != o.id || h[1] != "blob" {
			readErr = fmt.Errorf("invalid Git blob header")
			break
		}
		size, e := strconv.ParseInt(h[2], 10, 64)
		if e != nil || size < 0 || size > 64*1024*1024 {
			readErr = fmt.Errorf("source file size invalid: %s", o.ref)
			break
		}
		total += size
		if total > 512*1024*1024 {
			readErr = fmt.Errorf("template source exceeds limit")
			break
		}
		b := make([]byte, int(size))
		if _, e = io.ReadFull(r, b); e != nil {
			readErr = e
			break
		}
		if x, e := r.ReadByte(); e != nil || x != '\n' {
			readErr = fmt.Errorf("invalid Git blob delimiter")
			break
		}
		out[o.ref] = sourceFile{b, o.mode}
	}
	if readErr != nil {
		cmd.Process.Kill()
	}
	e = cmd.Wait()
	if readErr != nil {
		return nil, readErr
	}
	if e != nil {
		return nil, fmt.Errorf("git cat-file: %w: %s", e, stderr.String())
	}
	for _, ref := range sortedKeys(out) {
		f := out[ref]
		if f.mode != 0 {
			continue
		}
		target := path.Clean(path.Join(path.Dir(ref), string(f.data)))
		name := strings.Split(ref, "/")[2]
		if target != ".agents/skills/"+name {
			return nil, fmt.Errorf("unsafe Skill projection: %s", ref)
		}
		count := 0
		for _, source := range sortedKeys(out) {
			if strings.HasPrefix(source, target+"/") {
				original := out[source]
				if original.mode == 0 {
					return nil, fmt.Errorf("nested projection symlink")
				}
				destination := ref + strings.TrimPrefix(source, target)
				if _, exists := out[destination]; exists {
					return nil, fmt.Errorf("Skill projection collision")
				}
				out[destination] = original
				count++
			}
		}
		if count == 0 {
			return nil, fmt.Errorf("missing canonical Skill projection: %s", ref)
		}
		delete(out, ref)
	}
	return out, nil
}
func projectionLink(ref string) bool {
	p := strings.Split(ref, "/")
	return len(p) == 3 && (p[0] == ".codex" || p[0] == ".cursor" || p[0] == ".pi") && p[1] == "skills" && !strings.HasPrefix(p[2], ".")
}
func stringsFrom(v any) []string {
	switch a := v.(type) {
	case []string:
		return a
	case []any:
		out := []string{}
		for _, x := range a {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
func stringSet(a []string) map[string]bool {
	m := map[string]bool{}
	for _, s := range a {
		m[s] = true
	}
	return m
}
func beneath(ref, prefix string) bool {
	return ref == strings.TrimSuffix(prefix, "/") || strings.HasPrefix(ref, strings.TrimSuffix(prefix, "/")+"/")
}
func includedByManifest(ref string, m map[string]any, init bool) bool {
	root := strings.Split(ref, "/")[0]
	for _, allow := range stringsFrom(m["allowFiles"]) {
		if beneath(ref, allow) {
			return true
		}
	}
	allowed := stringSet(stringsFrom(m["allowRootEntries"]))
	files := stringSet(stringsFrom(m["allowRootFiles"]))
	if len(allowed)+len(files) > 0 && !allowed[root] && !files[ref] {
		return false
	}
	for _, key := range []string{"excludeRootEntries", "excludeRootFiles"} {
		if stringSet(stringsFrom(m[key]))[root] {
			return false
		}
	}
	for _, p := range stringsFrom(m["excludePaths"]) {
		if beneath(ref, p) {
			return false
		}
	}
	for _, suffix := range stringsFrom(m["excludePathSuffixes"]) {
		for _, part := range strings.Split(ref, "/") {
			if strings.HasSuffix(part, suffix) {
				return false
			}
		}
	}
	if init {
		for _, key := range []string{"initExcludeRootEntries", "initExcludeRootFiles"} {
			if stringSet(stringsFrom(m[key]))[root] {
				return false
			}
		}
		for _, p := range append(stringsFrom(m["initExcludePaths"]), stringsFrom(m["exampleDocPaths"])...) {
			if beneath(ref, p) {
				return false
			}
		}
	}
	return true
}
func encodedFile(data []byte, mode uint32, own string) File {
	return File{Data: base64.StdEncoding.EncodeToString(data), Digest: safefs.Digest(data), Mode: mode, Ownership: own}
}
func ownership(profile, ref string, m map[string]any) string {
	if profile == "spec" {
		policy, _ := m["ownershipPolicy"].(map[string]any)
		if rules, ok := policy["rules"].([]any); ok {
			for _, item := range rules {
				rule, _ := item.(map[string]any)
				pattern, _ := rule["pattern"].(string)
				own, _ := rule["ownership"].(string)
				if matchesOwnership(ref, pattern) {
					return own
				}
			}
		}
		if own, ok := policy["default"].(string); ok {
			return own
		}
		return "managed"
	}
	preserved := ref == "README.md" || ref == "CONTEXT.md" || ref == "DESIGN.md" || ref == ".template-spec/agents/issue-tracker.md"
	user := ref == "README.md" || ref == "DESIGN.md"
	if profile == "design" {
		managed := false
		for _, prefix := range []string{".agents/skills", ".codex/skills", ".cursor/skills", ".pi/skills", "scripts", ".template-spec/agents", ".template-spec/process", ".template-spec/templates", ".template-spec/user-guide", ".template-spec/architecture/templates"} {
			managed = managed || beneath(ref, prefix)
		}
		for _, p := range []string{".gitignore", ".nvmrc", "AGENTS.md", "CLAUDE.md", ".cursorrules", "skills-lock.json", "yss-project.yaml", "CONTEXT.md"} {
			managed = managed || ref == p
		}
		user = user || !managed
	} else {
		for _, prefix := range []string{".work", "docs/.scratch", "docs/reviews", "docs/releases", "docs/requirements", "docs/implementation", "docs/api", "docs/adr"} {
			user = user || beneath(ref, prefix)
		}
	}
	if user {
		return "user-owned"
	}
	if preserved {
		return "managed-customizable"
	}
	return "managed"
}
func matchesOwnership(ref, p string) bool {
	if strings.HasSuffix(p, "/**") {
		return beneath(ref, strings.TrimSuffix(p, "/**"))
	}
	if strings.HasSuffix(p, "/*") {
		base := strings.TrimSuffix(p, "/*")
		return path.Dir(ref) == base
	}
	return ref == p
}
func contentHash(b *Bundle) string {
	clone := *b
	clone.BundleHash = ""
	h := sha256.New()
	if err := json.NewEncoder(jsonHashWriter{h}).Encode(&clone); err != nil {
		return safefs.Digest(nil)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Compact Encoder output adds one newline to the exact Marshal bytes. Hash
// directly from its buffer instead of allocating a second full JSON copy.
type jsonHashWriter struct{ hash.Hash }

func (w jsonHashWriter) Write(p []byte) (int, error) {
	_, err := w.Hash.Write(bytes.TrimSuffix(p, []byte("\n")))
	return len(p), err
}
func sortedKeys[V any](m map[string]V) []string {
	a := make([]string, 0, len(m))
	for k := range m {
		a = append(a, k)
	}
	sort.Strings(a)
	return a
}

func validatePolicy(p Policy) error {
	if len(stringsFrom(p.Manifest["allowRootEntries"]))+len(stringsFrom(p.Manifest["allowRootFiles"])) == 0 {
		return fmt.Errorf("policy must explicitly select distribution roots")
	}
	for _, key := range []string{"allowRootEntries", "allowRootFiles", "allowFiles", "excludeRootEntries", "excludeRootFiles", "excludePaths", "renderPaths", "exampleDocPaths", "initExcludeRootEntries", "initExcludeRootFiles", "initExcludePaths", "instanceForbiddenPaths"} {
		for _, ref := range stringsFrom(p.Manifest[key]) {
			if ref == ".git" {
				continue
			}
			if e := safefs.ValidateRef(ref); e != nil {
				return fmt.Errorf("policy %s: %w", key, e)
			}
		}
	}
	if ownership, ok := p.Manifest["ownershipPolicy"].(map[string]any); ok {
		types := stringSet([]string{"managed", "managed-customizable", "generated", "user-owned", "protected"})
		if !types[fmt.Sprint(ownership["default"])] {
			return fmt.Errorf("ownership default invalid")
		}
		seen := map[string]bool{}
		if rules, ok := ownership["rules"].([]any); ok {
			for _, v := range rules {
				r, ok := v.(map[string]any)
				if !ok {
					return fmt.Errorf("ownership rule invalid")
				}
				pattern, _ := r["pattern"].(string)
				base := strings.TrimSuffix(strings.TrimSuffix(pattern, "/**"), "/*")
				if base != ".git" {
					if e := safefs.ValidateRef(base); e != nil {
						return e
					}
				}
				if seen[pattern] || !types[fmt.Sprint(r["ownership"])] {
					return fmt.Errorf("ownership rule conflict")
				}
				seen[pattern] = true
			}
		}
	}
	return nil
}
