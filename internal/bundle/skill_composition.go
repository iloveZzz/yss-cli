package bundle

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/safefs"
)

type SkillSource struct {
	State             string `json:"sourceState,omitempty"`
	Tree              string `json:"sourceTree,omitempty"`
	Root              string `json:"-"`
	SourcePath        string `json:"sourcePath"`
	Commit            string `json:"templateCommit"`
	ConfigurationPath string `json:"configurationPath"`
	ConfigurationHash string `json:"configurationHash"`
}
type skillItem struct {
	ID             string            `json:"id"`
	Source         string            `json:"source"`
	Target         string            `json:"target"`
	Strategy       string            `json:"strategy"`
	Patch          string            `json:"patch"`
	SourceTreeHash string            `json:"source_tree_sha256"`
	Files          []string          `json:"files"`
	FileModes      map[string]string `json:"file_modes"`
	Replacements   []struct {
		File  string `json:"file"`
		From  string `json:"from"`
		To    string `json:"to"`
		Count *int   `json:"count"`
	} `json:"replacements"`
}
type skillProfile struct {
	Materialization string      `json:"materialization"`
	Exact           []skillItem `json:"exact"`
	Adapted         []skillItem `json:"adapted"`
	Local           []string    `json:"local_only"`
	Excluded        []string    `json:"excluded"`
	Upstream        []string    `json:"upstream"`
	Retired         []string    `json:"retired"`
}
type profileSkillSourceLock struct {
	SchemaVersion     int    `json:"schemaVersion"`
	Profile           string `json:"profile"`
	Commit            string `json:"sourceCommit"`
	State             string `json:"sourceState"`
	ConfigurationPath string `json:"configurationPath"`
	ConfigurationHash string `json:"configurationHash"`
	SkillsDigest      string `json:"skillsDigest"`
}

func committedSkillFile(ctx context.Context, root, commit, ref string) ([]byte, error) {
	if !fullCommit.MatchString(commit) {
		return nil, fmt.Errorf("shared source requires full commit")
	}
	if e := safefs.ValidateRef(ref); e != nil {
		return nil, e
	}
	listing, e := git(ctx, root, "ls-tree", commit, "--", ref)
	if e != nil {
		return nil, e
	}
	fields := strings.Fields(string(listing))
	if len(fields) != 4 || fields[1] != "blob" || (fields[0] != "100644" && fields[0] != "100755") {
		return nil, fmt.Errorf("shared source is not a committed regular file: %s", ref)
	}
	return git(ctx, root, "show", commit+":"+ref)
}
func compositionDigest(files map[string]sourceFile) string {
	h := sha256.New()
	for _, ref := range sortedKeys(files) {
		f := files[ref]
		fmt.Fprintf(h, "%s\x00%04o\x00", ref, f.mode)
		h.Write(f.data)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
func skillTreeDigest(files map[string]sourceFile) string {
	h := sha256.New()
	for _, ref := range sortedKeys(files) {
		h.Write([]byte(ref))
		h.Write([]byte{0})
		h.Write(files[ref].data)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
func ignoredSkillCache(ref string) bool {
	for _, part := range strings.Split(ref, "/") {
		if part == "__pycache__" || part == ".DS_Store" || strings.HasSuffix(part, ".pyc") || strings.HasSuffix(part, ".pyo") || strings.HasSuffix(part, ".iml") {
			return true
		}
	}
	return false
}
func applySkillPatch(ctx context.Context, files map[string]sourceFile, patchBytes []byte) (map[string]sourceFile, error) {
	root, e := os.MkdirTemp("", "yss-skill-patch-")
	if e != nil {
		return nil, e
	}
	defer os.RemoveAll(root)
	root, e = filepath.EvalSymlinks(root)
	if e != nil {
		return nil, e
	}
	for ref, f := range files {
		file, e := safefs.Path(root, ref)
		if e != nil {
			return nil, e
		}
		if e = os.MkdirAll(filepath.Dir(file), 0755); e != nil {
			return nil, e
		}
		if e = os.WriteFile(file, f.data, os.FileMode(f.mode)); e != nil {
			return nil, e
		}
		if e = os.Chmod(file, os.FileMode(f.mode)); e != nil {
			return nil, e
		}
	}
	run := func(args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "core.autocrlf=false", "apply"}, args...)...)
		cmd.Dir = root
		cmd.Stdin = bytes.NewReader(patchBytes)
		return cmd.CombinedOutput()
	}
	stat, e := run("--numstat", "-z")
	if e != nil {
		return nil, fmt.Errorf("skill patch invalid: %w: %s", e, stat)
	}
	for _, record := range bytes.Split(stat, []byte{0}) {
		if len(record) == 0 {
			continue
		}
		parts := bytes.SplitN(record, []byte{'\t'}, 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("skill patch rename unsupported")
		}
		ref := string(parts[2])
		if e := safefs.ValidateRef(ref); e != nil {
			return nil, e
		}
		if strings.Split(ref, "/")[0] == ".git" {
			return nil, fmt.Errorf("unsafe skill patch path")
		}
	}
	if o, e := run("--binary", "--whitespace=nowarn", "-"); e != nil {
		return nil, fmt.Errorf("skill patch failed: %w: %s", e, o)
	}
	out := map[string]sourceFile{}
	e = filepath.WalkDir(root, func(file string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		ref, e := filepath.Rel(root, file)
		if e != nil {
			return e
		}
		ref = filepath.ToSlash(ref)
		if ignoredSkillCache(ref) {
			return nil
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("skill patch generated link or special file: %s", ref)
		}
		mode := uint32(info.Mode().Perm())
		if mode != 0644 && mode != 0755 {
			return fmt.Errorf("invalid skill mode")
		}
		data, e := os.ReadFile(file)
		if e != nil {
			return e
		}
		out[ref] = sourceFile{data: data, mode: mode}
		return nil
	})
	return out, e
}

func composeCommittedSkills(ctx context.Context, s Source, raw map[string]sourceFile) (map[string]any, error) {
	shared := s.SkillsSource
	if s.Profile != "backend" {
		return nil, fmt.Errorf("skill composition only supports backend")
	}
	if shared.Root == "" || shared.ConfigurationPath != ".template-source/profile-skill-sync.json" {
		return nil, fmt.Errorf("shared skill source root/configuration missing")
	}
	sharedObject, sharedState, e := sourceObject(ctx, Source{Root: shared.Root, Commit: shared.Commit, State: shared.State, Tree: shared.Tree})
	if e != nil {
		return nil, e
	}
	object, state, e := sourceObject(ctx, s)
	if e != nil {
		return nil, e
	}
	if state != sharedState {
		return nil, fmt.Errorf("mixed committed/worktree skill sources")
	}
	cfgBytes, e := committedSkillFile(ctx, shared.Root, sharedObject, shared.ConfigurationPath)
	if e != nil {
		return nil, e
	}
	if safefs.Digest(cfgBytes) != shared.ConfigurationHash {
		return nil, fmt.Errorf("shared skill configuration digest mismatch")
	}
	var cfg struct {
		SchemaVersion int                     `json:"schema_version"`
		Profiles      map[string]skillProfile `json:"profiles"`
	}
	if e = json.Unmarshal(cfgBytes, &cfg); e != nil {
		return nil, e
	}
	p, ok := cfg.Profiles[s.Profile]
	if cfg.SchemaVersion != 1 || !ok || p.Materialization != "generated" {
		return nil, fmt.Errorf("generated skill profile missing")
	}
	lockBytes, e := committedSkillFile(ctx, s.Root, object, ".template-source/profile-skills-source.json")
	if e != nil {
		return nil, e
	}
	var lock profileSkillSourceLock
	if e = json.Unmarshal(lockBytes, &lock); e != nil {
		return nil, e
	}
	if lock.SchemaVersion != 1 || lock.Profile != s.Profile || lock.State != sharedState || lock.Commit != shared.Commit || lock.ConfigurationPath != shared.ConfigurationPath || lock.ConfigurationHash != shared.ConfigurationHash {
		return nil, fmt.Errorf("profile skill source lock mismatch")
	}
	items := append(append([]skillItem{}, p.Exact...), p.Adapted...)
	seen := map[string]bool{}
	for _, id := range append(append(append(append([]string{}, p.Local...), p.Excluded...), p.Upstream...), p.Retired...) {
		if seen[id] {
			return nil, fmt.Errorf("duplicate skill classification: %s", id)
		}
		seen[id] = true
	}
	allows := []string{}
	for _, item := range items {
		if item.ID == "" || seen[item.ID] {
			return nil, fmt.Errorf("duplicate skill classification: %s", item.ID)
		}
		seen[item.ID] = true
		source := item.Source
		if source == "" {
			source = ".agents/skills/" + item.ID
		}
		if e := safefs.ValidateRef(source); e != nil {
			return nil, e
		}
		if !strings.HasPrefix(source, ".agents/skills/") && !strings.HasPrefix(source, ".claude/skills/") && !strings.HasPrefix(source, ".codex/skills/") {
			return nil, fmt.Errorf("unsupported shared skill source")
		}
		allows = append(allows, source)
	}
	all, e := readGitFiles(ctx, shared.Root, sharedObject, map[string]any{"allowFiles": allows, "allowRootFiles": []string{shared.ConfigurationPath}})
	if e != nil {
		return nil, e
	}
	output := map[string]sourceFile{}
	for _, item := range items {
		source := item.Source
		if source == "" {
			source = ".agents/skills/" + item.ID
		}
		target := item.Target
		if target == "" {
			target = ".agents/skills/" + item.ID
		}
		if e := safefs.ValidateRef(target); e != nil {
			return nil, e
		}
		parts := strings.Split(target, "/")
		if len(parts) != 3 || (parts[0] != ".agents" && parts[0] != ".codex") || parts[1] != "skills" || parts[2] != item.ID {
			return nil, fmt.Errorf("unsafe generated skill target")
		}
		files := map[string]sourceFile{}
		for ref, f := range all {
			if strings.HasPrefix(ref, source+"/") && !ignoredSkillCache(ref) {
				files[strings.TrimPrefix(ref, source+"/")] = f
			}
		}
		if len(files) == 0 {
			return nil, fmt.Errorf("shared skill source missing: %s", item.ID)
		}
		if len(item.Files) > 0 {
			return nil, fmt.Errorf("partial skill adaptation cannot be composed")
		}
		switch item.Strategy {
		case "":
		case "replace":
			for _, r := range item.Replacements {
				f, ok := files[r.File]
				count := 1
				if r.Count != nil {
					count = *r.Count
				}
				if !ok || r.From == "" || count < 1 || strings.Count(string(f.data), r.From) != count {
					return nil, fmt.Errorf("skill replacement baseline mismatch: %s", item.ID)
				}
				f.data = []byte(strings.ReplaceAll(string(f.data), r.From, r.To))
				files[r.File] = f
			}
		case "patch":
			if skillTreeDigest(files) != item.SourceTreeHash {
				return nil, fmt.Errorf("skill patch baseline mismatch: %s", item.ID)
			}
			patchBytes, e := committedSkillFile(ctx, shared.Root, sharedObject, item.Patch)
			if e != nil {
				return nil, e
			}
			files, e = applySkillPatch(ctx, files, patchBytes)
			if e != nil {
				return nil, e
			}
		default:
			return nil, fmt.Errorf("unsupported skill adaptation: %s", item.Strategy)
		}
		for ref, f := range files {
			if mode, ok := item.FileModes[ref]; ok {
				switch mode {
				case "0644":
					f.mode = 0644
				case "0755":
					f.mode = 0755
				default:
					return nil, fmt.Errorf("unsupported profile skill mode")
				}
			}
			dest := target + "/" + ref
			if e := safefs.ValidateRef(dest); e != nil {
				return nil, e
			}
			if _, ok := output[dest]; ok {
				return nil, fmt.Errorf("generated skill overlap")
			}
			output[dest] = f
		}
	}
	digest := compositionDigest(output)
	if digest != lock.SkillsDigest {
		return nil, fmt.Errorf("composed skill digest mismatch")
	}
	for ref, f := range output {
		if old, exists := raw[ref]; exists && (!bytes.Equal(old.data, f.data) || old.mode != f.mode) {
			return nil, fmt.Errorf("tracked shared skill differs from composition: %s", ref)
		}
		raw[ref] = f
		if strings.HasPrefix(ref, ".agents/skills/") {
			suffix := strings.TrimPrefix(ref, ".agents/")
			projectionRuntimes := []string{".codex", ".cursor", ".pi", ".claude"}
			if lockFile, ok := raw["skills-lock.json"]; ok {
				var projectionLock struct {
					Roots []string `json:"projectionRoots"`
				}
				if json.Unmarshal(lockFile.data, &projectionLock) == nil {
					for _, root := range projectionLock.Roots {
						if root == ".workbuddy/skills" {
							projectionRuntimes = append(projectionRuntimes, ".workbuddy")
						}
					}
				}
			}
			for _, runtime := range projectionRuntimes {
				projection := path.Join(runtime, suffix)
				if old, ok := raw[projection]; ok && (!bytes.Equal(old.data, f.data) || old.mode != f.mode) {
					return nil, fmt.Errorf("tracked skill projection differs: %s", projection)
				}
				raw[projection] = f
			}
		}
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	sort.Strings(ids)
	provenance := map[string]any{"sourceTemplateCommit": shared.Commit, "configurationPath": shared.ConfigurationPath, "configurationHash": shared.ConfigurationHash, "skillsDigest": digest, "skills": ids}
	if sharedState == "working-tree" {
		provenance["sourceState"] = sharedState
		provenance["sourceTree"] = sharedObject
	}
	return provenance, nil
}
