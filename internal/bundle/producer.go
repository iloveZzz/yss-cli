package bundle

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/safefs"
)

type InstallationPolicy struct {
	Skills          []string            `json:"skills"`
	Files           []string            `json:"files"`
	Entries         map[string]string   `json:"entries,omitempty"`
	SkillReferences map[string][]string `json:"skillReferences,omitempty"`
}
type Policy struct {
	SchemaVersion int                           `json:"schemaVersion"`
	Profile       string                        `json:"profile"`
	Manifest      map[string]any                `json:"manifest"`
	Installations map[string]InstallationPolicy `json:"installations"`
}
type StorageReport struct {
	StorageFormatVersion int    `json:"storageFormatVersion"`
	Filename             string `json:"filename"`
	SHA256               string `json:"sha256"`
	CompressedBytes      int    `json:"compressedBytes"`
	JSONBytes            int    `json:"jsonBytes"`
}

func git(ctx context.Context, root string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %v: %w", args, err)
	}
	return out, nil
}
func BuildRoot(ctx context.Context, root string) (map[string]*Bundle, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	head, err := git(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	commit := strings.TrimSpace(string(head))
	if !commitPattern.MatchString(commit) {
		return nil, fmt.Errorf("模板提交不合法")
	}
	status, err := git(ctx, root, "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return nil, err
	}
	state := "committed"
	if len(status) > 0 {
		state = "working-tree"
	}
	listed, err := git(ctx, root, "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	committed, err := git(ctx, root, "ls-tree", "-rz", "HEAD")
	if err != nil {
		return nil, err
	}
	blobs := map[string]string{}
	modes := map[string]string{}
	for _, entry := range strings.Split(string(committed), "\x00") {
		parts := strings.SplitN(entry, "\t", 2)
		if len(parts) == 2 {
			fields := strings.Fields(parts[0])
			if len(fields) == 3 {
				blobs[parts[1]] = fields[2]
				modes[parts[1]] = fields[0]
			}
		}
	}
	source := map[string]File{}
	links := map[string]string{}
	if _, err = safefs.Path(root, ".bundle-source-check"); err != nil {
		return nil, err
	}
	for _, ref := range strings.Split(string(listed), "\x00") {
		if ref == "" {
			continue
		}
		if err = safefs.ValidateRef(ref); err != nil {
			return nil, err
		}
		p := filepath.Join(root, filepath.FromSlash(ref))
		st, e := os.Lstat(p)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return nil, e
		}
		if st.IsDir() {
			continue
		}
		resolved, e := filepath.EvalSymlinks(p)
		if e != nil {
			return nil, e
		}
		if resolved != p {
			rel, e := filepath.Rel(root, resolved)
			if e != nil {
				return nil, e
			}
			rel = filepath.ToSlash(rel)
			parts := strings.Split(ref, "/")
			if len(parts) < 3 || parts[1] != "skills" || !stringSet([]string{".codex", ".cursor", ".pi", ".claude", ".workbuddy"})[parts[0]] || rel != ".agents/skills/"+strings.Join(parts[2:], "/") {
				return nil, fmt.Errorf("不安全技能投影 %s", ref)
			}
			if state == "committed" {
				link, e := os.Readlink(p)
				if e != nil {
					state = "working-tree"
				} else if blobHash([]byte(link)) != blobs[ref] {
					state = "working-tree"
				}
			}
			st, e = os.Stat(resolved)
			if e != nil {
				return nil, e
			}
			if st.IsDir() {
				links[ref] = rel
				continue
			}
			p = resolved
		}

		if !st.Mode().IsRegular() {
			return nil, fmt.Errorf("不支持文件类型 %s", ref)
		}
		data, e := os.ReadFile(p)
		if e != nil {
			return nil, e
		}
		if state == "committed" && resolved == filepath.Join(root, filepath.FromSlash(ref)) && blobHash(data) != blobs[ref] {
			state = "working-tree"
		}
		mode := uint32(0644)
		if st.Mode().Perm()&0111 != 0 {
			mode = 0755
		}
		if state == "committed" && resolved == filepath.Join(root, filepath.FromSlash(ref)) && ((mode == 0755 && modes[ref] != "100755") || (mode == 0644 && modes[ref] != "100644")) {
			state = "working-tree"
		}
		source[ref] = encodedFile(data, mode, "managed")
	}
	for target, canonical := range links {
		for ref, f := range source {
			if beneath(ref, canonical) {
				source[target+strings.TrimPrefix(ref, canonical)] = f
			}
		}
	}
	bundles := map[string]*Bundle{}
	for _, profile := range []string{"spec", "backend"} {
		prefix := ""
		if profile == "backend" {
			prefix = "profiles/backend/"
		}
		policyRef := prefix + ".template-source/distribution/bundle-profile.json"
		policyFile, ok := source[policyRef]
		if !ok {
			return nil, fmt.Errorf("缺少分发清单 %s", policyRef)
		}
		raw, _ := base64.StdEncoding.DecodeString(policyFile.Data)
		var policy Policy
		if err = json.Unmarshal(raw, &policy); err != nil {
			return nil, err
		}
		if policy.Profile != profile || policy.SchemaVersion != 6 {
			return nil, fmt.Errorf("清单身份不合法 %s", policyRef)
		}
		files := map[string]File{}
		add := func(ref string, f File) {
			if includedByManifest(ref, policy.Manifest, false) {
				f.Ownership = ownership(profile, ref, policy.Manifest)
				files[ref] = f
			}
		}
		for ref, f := range source {
			if profile == "spec" {
				add(ref, f)
			} else if inKernelScope(ref) || strings.HasPrefix(ref, ".template-source/") {
				add(ref, f)
			}
		}
		if profile == "backend" {
			for _, overlay := range []string{"profiles/_specialist/", prefix} {
				for ref, f := range source {
					if strings.HasPrefix(ref, overlay) {
						add(strings.TrimPrefix(ref, overlay), f)
					}
				}
			}
		}

		manifestRaw, _ := json.Marshal(policy.Manifest)
		snapshotRaw, _ := json.Marshal(files)
		snapshot := safefs.Digest(snapshotRaw)
		version := "snapshot:" + snapshot
		if state == "working-tree" {
			version = "worktree:" + snapshot
		}
		b := &Bundle{SchemaVersion: 6, Profile: profile, TemplateVersion: version, TemplateCommit: commit, SourceState: state, SnapshotHash: snapshot, ManifestHash: safefs.Digest(manifestRaw), SourcePolicy: PolicyProvenance{Path: policyRef, Digest: policyFile.Digest, Kind: state}, Producer: Provenance{Version: "bundle6", SourceState: state}, Manifest: policy.Manifest, Files: files, Initial: map[string]File{}, Installations: map[string]Installation{}, SkillRequirements: map[string]Requirement{}}
		for _, kind := range []string{"project", "skills"} {
			cfg, ok := policy.Installations[kind]
			if !ok {
				return nil, fmt.Errorf("缺少安装清单 %s", kind)
			}
			initial := map[string]File{}
			for _, ref := range cfg.Files {
				f, ok := files[ref]
				if !ok {
					return nil, fmt.Errorf("安装资源缺失 %s", ref)
				}
				initial[ref] = f
			}
			for _, skill := range cfg.Skills {
				found := false
				for ref, f := range files {
					if skillPath(ref, skill) {
						initial[ref] = f
						found = true
					}
				}
				if !found {
					return nil, fmt.Errorf("安装技能缺失 %s", skill)
				}
				for _, ref := range cfg.SkillReferences[skill] {
					f, ok := files[ref]
					if !ok {
						return nil, fmt.Errorf("技能引用缺失 %s", ref)
					}
					initial[ref] = f
				}
			}

			for target, entry := range cfg.Entries {
				if err = safefs.ValidateRef(target); err != nil {
					return nil, err
				}
				f, ok := source[prefix+entry]
				if !ok {
					f, ok = source[entry]
				}
				if !ok {
					return nil, fmt.Errorf("入口缺失 %s", entry)
				}
				f.Ownership = "managed-customizable"
				initial[target] = f
			}
			skills := []string{}
			for ref := range initial {
				if strings.HasPrefix(ref, ".agents/skills/") && strings.HasSuffix(ref, "/SKILL.md") && strings.Count(ref, "/") == 3 {
					skills = append(skills, strings.Split(ref, "/")[2])
				}
			}
			sort.Strings(skills)
			d := map[string]any{"installation": kind, "installedSkills": skills, "runtimes": []string{}}
			b.Installations[kind] = Installation{Initial: initial, Distribution: d, SkillReferences: cfg.SkillReferences}
			if kind == "project" {
				b.Initial = initial
				b.Distribution = d
			}
			for skill, refs := range cfg.SkillReferences {
				b.SkillRequirements[skill] = Requirement{Paths: refs}
			}
		}
		for skill, req := range b.SkillRequirements {
			deps := map[string]bool{}
			prefix := ".agents/skills/" + skill + "/"
			for ref, f := range files {
				if strings.HasPrefix(ref, prefix) && strings.HasSuffix(ref, ".md") {
					raw, _ := base64.StdEncoding.DecodeString(f.Data)
					for _, match := range skillDependency.FindAllSubmatch(raw, -1) {
						dep := string(match[1])
						if dep != skill {
							if _, ok := files[".agents/skills/"+dep+"/SKILL.md"]; ok {
								deps[dep] = true
							}
						}
					}
				}
			}
			req.Skills = sortedKeys(deps)
			b.SkillRequirements[skill] = req
		}
		for kind, item := range b.Installations {
			names := stringsFrom(item.Distribution["installedSkills"])
			selected, e := b.SkillFiles(names)
			if e != nil {
				return nil, e
			}
			for ref, f := range selected {
				item.Initial[ref] = f
			}
			b.Installations[kind] = item
		}
		b.BundleHash = Hash(b)
		if err = validate(b); err != nil {
			return nil, err
		}
		bundles[profile] = b
	}
	return bundles, nil
}
func inKernelScope(ref string) bool {
	for _, p := range []string{".agents/skills", ".template-spec/process", ".template-spec/templates"} {
		if beneath(ref, p) {
			return true
		}
	}
	return false
}
func WriteBuilt(out string, bundles map[string]*Bundle) (*StorageReport, error) {
	for _, b := range bundles {
		if err := validate(b); err != nil {
			return nil, err
		}
	}
	raw, err := pack(bundles)
	if err != nil {
		return nil, err
	}
	roundtrip, err := unpack(raw)
	if err != nil {
		return nil, err
	}
	for _, b := range roundtrip {
		if err = validate(b); err != nil {
			return nil, err
		}
	}
	if len(raw) > maxSnapshotBytes {
		return nil, fmt.Errorf("模板超过限制")
	}
	var buf bytes.Buffer
	z, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	z.Header.OS = 255
	if _, err = z.Write(raw); err != nil {
		return nil, err
	}
	if err = z.Close(); err != nil {
		return nil, err
	}
	if buf.Len() > MaxArchiveBytes {
		return nil, fmt.Errorf("模板归档超过限制")
	}
	if err = os.MkdirAll(out, 0755); err != nil {
		return nil, err
	}
	target, err := safefs.Path(out, "bundles.json.gz")
	if err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(out, ".bundle-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		return nil, err
	}
	if err = tmp.Close(); err != nil {
		return nil, err
	}
	if err = os.Rename(tmp.Name(), target); err != nil {
		return nil, err
	}
	return &StorageReport{StorageFormatVersion: 6, Filename: "bundles.json.gz", SHA256: safefs.Digest(buf.Bytes()), CompressedBytes: buf.Len(), JSONBytes: len(raw)}, nil
}
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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
	if _, ok := m["ownershipPolicy"]; ok {
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

	for _, prefix := range []string{".work", "docs/.scratch", "docs/reviews", "docs/releases", "docs/requirements", "docs/implementation", "docs/api", "docs/adr"} {
		user = user || beneath(ref, prefix)
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

func blobHash(data []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(data))
	h.Write(data)
	return fmt.Sprintf("%x", h.Sum(nil))
}

var skillDependency = regexp.MustCompile(`\.agents/skills/([a-z0-9][a-z0-9-]*)`)
