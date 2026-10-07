package project

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/governance"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
	"github.com/iloveZzz/yss-cli/internal/worklayout"
	"go.yaml.in/yaml/v3"
)

// WorkLayoutMigration binds the source closure and the executable, without
// granting or carrying forward approval for rewritten evidence.
type WorkLayoutMigration struct {
	SourceRoot       string                       `json:"sourceRoot"`
	TargetRoot       string                       `json:"targetRoot"`
	Mapping          map[string]string            `json:"mapping"`
	DirectoryModes   map[string]uint32            `json:"directoryModes,omitempty"`
	Bindings         map[string]WorkLayoutBinding `json:"bindings,omitempty"`
	ExecutableDigest string                       `json:"executableDigest"`
	GitState         string                       `json:"gitState,omitempty"`
	GitIdentity      string                       `json:"gitIdentity,omitempty"`
	Verification     []string                     `json:"verification"`
}
type WorkLayoutBinding struct {
	TargetPath     string            `json:"targetPath"`
	Before         domain.Descriptor `json:"before"`
	After          domain.Descriptor `json:"after"`
	OriginalObject string            `json:"originalObject,omitempty"`
}

func workGitState(root string) (string, error) {
	if _, e := os.Lstat(filepath.Join(root, ".git")); os.IsNotExist(e) {
		return "", nil
	} else if e != nil {
		return "", e
	}
	var out bytes.Buffer
	for _, args := range [][]string{{"rev-parse", "HEAD"}, {"status", "--porcelain=v1", "-z", "--untracked-files=all"}, {"diff", "--cached", "--binary", "--no-ext-diff"}} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_PAGER=cat")
		b, e := cmd.Output()
		if e != nil {
			if args[0] == "rev-parse" {
				unborn := exec.Command("git", "-C", root, "symbolic-ref", "--quiet", "HEAD")
				if branch, be := unborn.Output(); be == nil {
					b = append([]byte("unborn:"), branch...)
				} else {
					return "", domain.Wrap("GIT_BASELINE", e)
				}
			} else {
				return "", domain.Wrap("GIT_BASELINE", e)
			}
		}
		out.Write(b)
		out.WriteByte(0)
	}
	indexCommand := exec.Command("git", "-C", root, "rev-parse", "--git-path", "index")
	index, e := indexCommand.Output()
	if e != nil {
		return "", domain.Wrap("GIT_BASELINE", e)
	}
	indexPath := strings.TrimSpace(string(index))
	if !filepath.IsAbs(indexPath) {
		indexPath = filepath.Join(root, indexPath)
	}
	indexBytes, e := os.ReadFile(indexPath)
	if e != nil && !os.IsNotExist(e) {
		return "", e
	}
	out.Write(indexBytes)
	return safefs.Digest(out.Bytes()), nil
}

func workInventory(root string) (map[string]domain.Descriptor, error) {
	out := map[string]domain.Descriptor{}
	skip := map[string]bool{".git": true, "node_modules": true, ".codegraph": true, ".graphify": true, "__pycache__": true, ".yss/transactions": true, ".yss/plans": true}
	var walk func(string) error
	walk = func(ref string) error {
		if skip[ref] || skip[filepath.Base(ref)] {
			return nil
		}
		file, e := safefs.Path(root, ref)
		if e != nil {
			return e
		}
		st, e := os.Lstat(file)
		if e != nil {
			return e
		}
		if st.IsDir() {
			if ref != ".git" {
				if _, e := os.Lstat(filepath.Join(file, ".git")); e == nil {
					return nil
				}
			}
			entries, e := os.ReadDir(file)
			if e != nil {
				return e
			}
			for _, entry := range entries {
				if e = walk(ref + "/" + entry.Name()); e != nil {
					return e
				}
			}
			return nil
		}
		if !st.Mode().IsRegular() {
			return domain.Fail("WORK_LAYOUT_PATH", "特殊文件: "+ref)
		}
		d, e := safefs.Describe(root, ref)
		if e != nil {
			return e
		}
		out[ref] = d
		return nil
	}
	entries, e := os.ReadDir(root)
	if e != nil {
		return nil, e
	}
	for _, entry := range entries {
		if e = walk(entry.Name()); e != nil {
			return nil, e
		}
	}
	return out, nil
}

func workProtected(v any) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	kind, _ := m["kind"].(string)
	status, _ := m["status"].(string)
	decision, _ := m["decision"].(string)
	return m["gate_id"] != nil || m["user_decision_id"] != nil || strings.Contains(kind, "approval") || strings.Contains(kind, "user-decision") || status == "approved" || status == "frozen" || decision == "approved"
}
func workHistoricalKey(key string) bool {
	switch key {
	case "history", "git_checkpoint", "original_response", "raw_response", "historical_refs", "archived_refs":
		return true
	}
	return false
}
func workReferences(value any, mapping map[string]string, key string) any {
	if workHistoricalKey(key) {
		return value
	}
	switch v := value.(type) {
	case string:
		if target, ok := workMappedReference(v, mapping); ok && workReferenceKey(key) {
			return target
		}
		return v
	case []any:
		for i, x := range v {
			v[i] = workReferences(x, mapping, key)
		}
		return v
	case map[string]any:
		for k, x := range v {
			v[k] = workReferences(x, mapping, k)
		}
		return v
	}
	return value
}
func workBindings(value any, candidates map[string][]byte, key string) {
	if workHistoricalKey(key) {
		return
	}
	switch v := value.(type) {
	case []any:
		for _, x := range v {
			workBindings(x, candidates, key)
		}
	case map[string]any:
		for k, x := range v {
			if ref, ok := x.(string); ok {
				digestKeys := []string{}
				if k == "ref" {
					digestKeys = []string{"digest", "sha256"}
				} else if strings.HasSuffix(k, "_ref") {
					base := strings.TrimSuffix(k, "_ref")
					digestKeys = []string{base + "_digest", base + "_sha256"}
				}
				for _, digestKey := range digestKeys {
					if old, ok := v[digestKey].(string); ok {
						if b, found := candidates[strings.SplitN(ref, "#", 2)[0]]; found {
							hash := safefs.Digest(b)
							if strings.HasPrefix(old, "sha256:") {
								hash = "sha256:" + hash
							}
							v[digestKey] = hash
						}
					}
				}
			}
			workBindings(x, candidates, k)
		}
	}
}

// BuildWorkLayout is deliberately separate from template and format upgrades.
func BuildWorkLayout(root, profile string) (*Plan, error) {
	return buildWorkLayout(root, profile, nil, "")
}

func buildWorkLayout(root, profile string, resolutions []Resolution, origin string) (*Plan, error) {
	id, e := Detect(root, profile, false)
	if e != nil {
		return nil, e
	}
	if id.Native == nil {
		return nil, domain.Fail("MIGRATION_REQUIRED", "先完成旧 CLI 到原生实例迁移，再迁移工作包目录")
	}
	if e = CheckLegacyState(id.Root, id.Profile.Name); e != nil {
		return nil, e
	}
	status, e := transaction.Status(id.Root)
	if e != nil {
		return nil, e
	}
	if len(status.Pending) > 0 || len(status.Preparations) > 0 {
		return nil, domain.Fail("INTERRUPTED", "先恢复未完成事务")
	}
	for _, ref := range []string{".yss/asset-transactions/active.json", ".yss/asset-transactions/lock", ".yss/asset-transactions/recovery.lock"} {
		p, e := safefs.Path(id.Root, ref)
		if e != nil {
			return nil, e
		}
		if _, e = os.Lstat(p); e == nil {
			return nil, domain.Fail("ASSET_TRANSACTION_PENDING", ref)
		} else if !os.IsNotExist(e) {
			return nil, e
		}
	}
	layout, e := worklayout.Read(id.Root)
	if e != nil {
		return nil, e
	}
	if layout.Root != worklayout.DefaultRoot && layout.Root != "docs/.scratch" {
		return nil, domain.Fail("WORK_LAYOUT_SOURCE", "本版只迁移 docs/.scratch 到 .work")
	}
	b, e := bundle.Load(id.Profile.Name)
	if e != nil {
		return nil, e
	}
	inputs, e := workInventory(id.Root)
	if e != nil {
		return nil, e
	}
	dirs, e := workSourceDirectories(id.Root, layout.Root)
	if e != nil {
		return nil, e
	}
	for ref, mode := range dirs {
		inputs[ref] = domain.Descriptor{Type: "directory", Mode: mode}
	}
	executable, e := domain.ExecutableDigest()
	if e != nil {
		return nil, e
	}
	git, e := workGitState(id.Root)
	if e != nil {
		return nil, e
	}
	gitIdentity, e := workGitIdentity(id.Root)
	if e != nil {
		return nil, e
	}
	p := &Plan{SchemaVersion: 2, ProtocolVersion: domain.ProtocolVersion, Command: "migrate", MigrationKind: "work-layout", Root: id.Root, Profile: id.Profile.Name, TemplateCommit: b.TemplateCommit, SnapshotHash: b.SnapshotHash, Inputs: inputs, Variables: map[string]string{}, Changes: []Change{}, Conflicts: []string{}, Preserved: []string{}, Assets: []AssetResult{}, Blockers: []Blocker{}}
	p.WorkLayout = &WorkLayoutMigration{SourceRoot: layout.Root, TargetRoot: worklayout.DefaultRoot, Mapping: map[string]string{}, DirectoryModes: dirs, ExecutableDigest: executable, GitState: git, GitIdentity: gitIdentity, Verification: []string{}}
	if layout.Root == worklayout.DefaultRoot {
		if e = verifyWorkLayoutCurrent(context.Background(), id.Root); e != nil {
			p.Blockers = append(p.Blockers, Blocker{Path: layout.Root, Code: "CANDIDATE_VERIFICATION", Reason: e.Error()})
		} else {
			p.ReadyToApply = true
			p.WorkLayout.Verification = []string{"context", "configured-root", "reference-closure", "applicable-governance"}
		}
		p.Digest = planDigest(p)
		return p, nil
	}
	mapping := p.WorkLayout.Mapping
	for ref := range dirs {
		mapping[ref] = worklayout.DefaultRoot + strings.TrimPrefix(ref, layout.Root)
	}
	for ref := range inputs {
		if strings.HasPrefix(ref, layout.Root+"/") {
			mapping[ref] = worklayout.DefaultRoot + strings.TrimPrefix(ref, layout.Root)
		}
	}
	overrides := map[string]Resolution{}
	for _, r := range resolutions {
		overrides[r.Path] = r
	}
	p.Resolutions = resolutions
	p.ResolvedFrom = origin
	block := func(ref, code, reason string) {
		p.Blockers = append(p.Blockers, Blocker{Path: ref, Code: code, Reason: reason})
	}
	for _, legacy := range []string{".scratch", "docs/requirements/tickets"} {
		for ref := range inputs {
			if strings.HasPrefix(ref, legacy+"/") {
				block(ref, "LEGACY_LAYOUT", "历史布局只读；先单独处理其迁移")
			}
		}
	}
	for ref := range inputs {
		if strings.HasPrefix(ref, worklayout.DefaultRoot+"/") {
			block(ref, "TARGET_EXISTS", "目标根已有资产，拒绝合并")
		}
	}
	if _, e := os.Lstat(filepath.Join(id.Root, worklayout.DefaultRoot)); e == nil {
		block(worklayout.DefaultRoot, "TARGET_EXISTS", "目标根已存在，拒绝合并")
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	for from, to := range mapping {
		if inputs[from].Type == "file" {
			ignored, e := workIgnoredTarget(id.Root, to)
			if e != nil {
				return nil, e
			}
			if ignored {
				block(from, "IGNORED_ASSET", "目标持久资产被 Git 忽略；先审阅并修复明确覆盖此范围的规则")
			}
		}
	}
	original := map[string][]byte{}
	candidate := map[string][]byte{}
	documents := map[string]*workDocument{}
	refs := []string{}
	for ref, d := range inputs {
		if d.Type == "file" {
			refs = append(refs, ref)
		}
	}
	sort.Strings(refs)
	for _, ref := range refs {
		raw, e := os.ReadFile(filepath.Join(id.Root, filepath.FromSlash(ref)))
		if e != nil {
			return nil, e
		}
		original[ref] = raw
		target := ref
		if mapped, ok := mapping[ref]; ok {
			target = mapped
		}
		data := append([]byte(nil), raw...)
		if r, ok := overrides[ref]; ok {
			data, e = resolutionBytes(r)
			if e != nil {
				return nil, e
			}
			if e = workRebindingBytes(ref, raw, data); e != nil {
				return nil, e
			}
		}
		if ref == worklayout.TrackerRef {
			continue
		}
		// Installation receipts and completed asset transactions are immutable
		// history. They remain guarded inputs, never candidate rewrite targets.
		if strings.HasPrefix(ref, ".yss/") {
			candidate[target] = raw
			continue
		}
		if managed, ok := id.Native.Managed[ref]; ok && managed.Applied == inputs[ref] && !strings.HasPrefix(ref, layout.Root+"/") {
			candidate[target] = data
			continue
		}
		if utf8.Valid(data) {
			doc, e := parseWorkDocument(data, filepath.Ext(ref))
			if e != nil && bytes.Contains(data, []byte(layout.Root+"/")) {
				block(ref, "UNPARSEABLE_REFERENCE", e.Error())
			} else if doc != nil {
				if workProtected(doc.value) || workProtectedPath(ref) {
					workCheckReferences(doc.value, candidate, inputs, mapping, target, layout.Root, "", func(code, reason string) { block(ref, code, reason) })
					if workAffectedReference(doc.value, mapping, "") || bytes.Contains(doc.body, []byte(layout.Root+"/")) {
						block(ref, "APPROVAL_REBIND_REQUIRED", "批准、冻结或原始决定不可改写；引用或批准依据变化需要新的当前证据")
					}
				} else {
					doc.value = workReferences(doc.value, mapping, "")
					workCheckReferences(doc.value, candidate, inputs, mapping, target, layout.Root, "", func(code, reason string) { block(ref, code, reason) })
					doc.body = workMarkdown(doc.body, ref, mapping, inputs, layout.Root, func(code, reason string) { block(ref, code, reason) })
					documents[target] = doc
				}
			} else if strings.EqualFold(filepath.Ext(ref), ".md") {
				if !workProtectedPath(ref) {
					data = workMarkdown(data, ref, mapping, inputs, layout.Root, func(code, reason string) { block(ref, code, reason) })
				}
			} else if !workProtectedPath(ref) && bytes.Contains(data, []byte(layout.Root+"/")) {
				block(ref, "UNKNOWN_REFERENCE", "代码、外部或动态引用不能机械改写")
			}
		}
		candidate[target] = data
	}
	for target, doc := range documents {
		raw, e := doc.encode()
		if e != nil {
			return nil, e
		}
		candidate[target] = raw
	}
	// Refresh mutable bindings in dependency order. Historical verification and
	// decisions are never recalculated or relabelled as fresh evidence.
	settled := false
	for n := 0; n <= len(documents); n++ {
		changed := false
		for target, doc := range documents {
			workBindings(doc.value, candidate, "")
			raw, e := doc.encode()
			if e != nil {
				return nil, e
			}
			if !bytes.Equal(raw, candidate[target]) {
				changed = true
				candidate[target] = raw
			}
		}
		if !changed {
			settled = true
			break
		}
	}
	if !settled {
		block(layout.Root, "BINDING_CYCLE", "摘要闭包不能收敛；保留原件并人工处理")
	}
	for ref, raw := range original {
		if strings.HasPrefix(ref, ".yss/") {
			continue
		}
		if _, ok := overrides[ref]; ok {
			continue
		}
		doc, _ := parseWorkDocument(raw, filepath.Ext(ref))
		if doc != nil && (workProtected(doc.value) || workProtectedPath(ref)) && workChangedBasis(doc.value, mapping, candidate, original, "") {
			block(ref, "APPROVAL_REBIND_REQUIRED", "不可变记录的依据字节改变，须通过现行决定与会签流程重新绑定")
		}
	}
	tracker := original[worklayout.TrackerRef]
	parts := regexp.MustCompile(`(?s)^---\r?\n(.*?)\r?\n---(?:\r?\n|$)(.*)$`).FindSubmatch(tracker)
	if parts == nil {
		return nil, domain.Fail("WORK_LAYOUT_CONFIG", "Tracker frontmatter 缺失")
	}
	var header yaml.Node
	if e = yaml.Unmarshal(parts[1], &header); e != nil {
		return nil, e
	}
	var front map[string]any
	if e = header.Decode(&front); e != nil {
		return nil, e
	}
	record, _ := front["tracker"].(map[string]any)
	record["root"] = worklayout.DefaultRoot
	legacy := []any{layout.Root, ".scratch", "docs/requirements/tickets"}
	record["legacy_roots"] = legacy
	workSyncNode(&header, front)
	encoded, e := yaml.Marshal(&header)
	if e != nil {
		return nil, e
	}
	body := string(parts[2])
	body = strings.ReplaceAll(body, "`"+layout.Root+"/`", "`"+worklayout.DefaultRoot+"/`")
	body = strings.ReplaceAll(body, layout.Root+"/<feature>", worklayout.DefaultRoot+"/<feature>")
	candidate[worklayout.TrackerRef] = append(append(append([]byte("---\n"), encoded...), []byte("---\n")...), []byte(body)...)
	for target, data := range candidate {
		before := inputs[target]
		if before.Type == "" {
			before = domain.Descriptor{Type: "missing"}
			p.Inputs[target] = before
		}
		after := domain.Descriptor{Type: "file", Digest: safefs.Digest(data), Mode: before.Mode}
		if after.Mode == 0 {
			for from, to := range mapping {
				if to == target {
					after.Mode = inputs[from].Mode
					break
				}
			}
		}
		if after.Mode == 0 {
			after.Mode = 0644
		}
		if before != after {
			p.Changes = append(p.Changes, Change{Path: target, Before: before, After: after, Data: base64.StdEncoding.EncodeToString(data), Ownership: "user-work-layout"})
		}
	}
	for from := range mapping {
		if inputs[from].Type == "directory" {
			target := mapping[from]
			before := domain.Descriptor{Type: "missing"}
			p.Inputs[target] = before
			p.Changes = append(p.Changes, Change{Path: target, Before: before, After: inputs[from], Ownership: "user-work-layout"})
		} else {
			p.Changes = append(p.Changes, Change{Path: from, Before: inputs[from], After: domain.Descriptor{Type: "missing"}, Ownership: "user-work-layout"})
		}
	}
	// Metadata baseline for the preserve-owned tracker records only this explicit customization.
	meta := *id.Native
	meta.Managed = map[string]Managed{}
	for ref, m := range id.Native.Managed {
		meta.Managed[ref] = m
	}
	if managed, ok := meta.Managed[worklayout.TrackerRef]; ok {
		managed.Applied.Digest = safefs.Digest(candidate[worklayout.TrackerRef])
		meta.Managed[worklayout.TrackerRef] = managed
	}
	baselineBytes, e := json.Marshal(meta.Managed)
	if e != nil {
		return nil, e
	}
	meta.BaselineDigest = safefs.Digest(baselineBytes)
	metaBytes, e := jsonBytes(&meta)
	if e != nil {
		return nil, e
	}
	if !bytes.Equal(metaBytes, original[MetadataFile]) {
		before := inputs[MetadataFile]
		p.Changes = append(p.Changes, Change{Path: MetadataFile, Before: before, After: domain.Descriptor{Type: "file", Digest: safefs.Digest(metaBytes), Mode: before.Mode}, Data: base64.StdEncoding.EncodeToString(metaBytes), Ownership: "runtime-metadata"})
	}
	dedup := map[string]bool{}
	uniqueBlockers := []Blocker{}
	for _, issue := range p.Blockers {
		key := issue.Path + "\x00" + issue.Code + "\x00" + issue.Reason
		if !dedup[key] {
			uniqueBlockers = append(uniqueBlockers, issue)
			dedup[key] = true
		}
	}
	p.Blockers = uniqueBlockers
	sort.Slice(p.Blockers, func(i, j int) bool {
		a, b := p.Blockers[i], p.Blockers[j]
		return a.Path+"\x00"+a.Code+"\x00"+a.Reason < b.Path+"\x00"+b.Code+"\x00"+b.Reason
	})
	blocked := map[string]string{}
	for _, issue := range p.Blockers {
		if blocked[issue.Path] != "" {
			blocked[issue.Path] += "; "
		}
		blocked[issue.Path] += issue.Code + ": " + issue.Reason
	}
	for ref, data := range original {
		target := ref
		if to, ok := mapping[ref]; ok {
			target = to
		}
		after := candidate[target]
		if _, moved := mapping[ref]; !moved && bytes.Equal(data, after) && blocked[ref] == "" {
			continue
		}
		d := domain.Descriptor{Type: "file", Digest: safefs.Digest(after), Mode: inputs[ref].Mode}
		a := AssetResult{Path: ref, Policy: "user-work-layout", Action: "migrate", Before: inputs[ref], Target: d, TargetPath: target, TargetData: base64.StdEncoding.EncodeToString(after), RuleID: "work-layout-v1", BaselineAvailable: true, Baseline: inputs[ref], BaselineData: base64.StdEncoding.EncodeToString(data), BaselineSource: "exact-input", Candidate: &Candidate{Data: base64.StdEncoding.EncodeToString(after), Digest: d.Digest, Clean: blocked[ref] == ""}}
		if reason := blocked[ref]; reason != "" {
			a.Action = "conflict"
			a.Reason = reason
			if !strings.Contains(reason, "MISSING_REFERENCE:") && !strings.Contains(reason, "TARGET_EXISTS:") && !strings.Contains(reason, "IGNORED_ASSET:") {
				a.Options = []string{"use-merged"}
			}
		}
		p.Assets = append(p.Assets, a)
	}
	p.WorkLayout.Bindings = map[string]WorkLayoutBinding{}
	for _, a := range p.Assets {
		if a.Before.Type == "file" {
			p.WorkLayout.Bindings[a.Path] = WorkLayoutBinding{TargetPath: a.TargetPath, Before: a.Before, After: a.Target, OriginalObject: "objects/" + a.Before.Digest}
		}
	}
	sort.Slice(p.Assets, func(i, j int) bool { return p.Assets[i].Path < p.Assets[j].Path })
	sort.Slice(p.Changes, func(i, j int) bool { return p.Changes[i].Path < p.Changes[j].Path })
	sort.Slice(p.Blockers, func(i, j int) bool {
		if p.Blockers[i].Path == p.Blockers[j].Path {
			return p.Blockers[i].Code < p.Blockers[j].Code
		}
		return p.Blockers[i].Path < p.Blockers[j].Path
	})
	p.ReadyToApply = len(p.Blockers) == 0
	if p.ReadyToApply {
		if e = verifyWorkLayoutOverlay(p); e != nil {
			block(layout.Root, "CANDIDATE_VERIFICATION", e.Error())
			p.ReadyToApply = false
		} else {
			p.WorkLayout.Verification = []string{"context", "configured-root", "reference-closure", "applicable-governance"}
		}
	}
	p.Digest = planDigest(p)
	return p, nil
}

func verifyWorkLayoutOverlay(p *Plan) error {
	staging, e := os.MkdirTemp("", "yss-work-layout-check-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(staging)
	staging, e = filepath.EvalSymlinks(staging)
	if e != nil {
		return e
	}
	for ref, d := range p.Inputs {
		if d.Type == "directory" {
			if e = os.MkdirAll(filepath.Join(staging, filepath.FromSlash(ref)), 0755); e != nil {
				return e
			}
			continue
		}
		if d.Type != "file" {
			continue
		}
		b, e := os.ReadFile(filepath.Join(p.Root, filepath.FromSlash(ref)))
		if e != nil {
			return e
		}
		dest := filepath.Join(staging, filepath.FromSlash(ref))
		if e = os.MkdirAll(filepath.Dir(dest), 0755); e != nil {
			return e
		}
		if e = os.WriteFile(dest, b, os.FileMode(d.Mode)); e != nil {
			return e
		}
	}
	for _, change := range p.Changes {
		file := filepath.Join(staging, filepath.FromSlash(change.Path))
		if change.After.Type == "directory" {
			if e = os.MkdirAll(file, 0755); e != nil {
				return e
			}
			if e = os.Chmod(file, os.FileMode(change.After.Mode)); e != nil {
				return e
			}
			continue
		}
		if change.After.Type == "missing" {
			if e = os.Remove(file); e != nil && !os.IsNotExist(e) {
				return e
			}
			continue
		}
		data, e := base64.StdEncoding.DecodeString(change.Data)
		if e != nil {
			return e
		}
		if e = os.MkdirAll(filepath.Dir(file), 0755); e != nil {
			return e
		}
		if e = os.WriteFile(file, data, os.FileMode(change.After.Mode)); e != nil {
			return e
		}
	}
	return verifyWorkLayoutCurrent(context.Background(), staging)
}
func verifyWorkLayoutCurrent(ctx context.Context, root string) error {
	l, e := worklayout.Read(root)
	if e != nil {
		return e
	}
	if l.Root != worklayout.DefaultRoot {
		return domain.Fail("WORK_LAYOUT_VERIFY", "目标配置根错误")
	}
	if _, e = governance.RunContext(ctx, "context", "verify", root, nil); e != nil {
		return e
	}
	inputs, e := workInventory(root)
	if e != nil {
		return e
	}
	id, e := Detect(root, "", false)
	if e != nil {
		return e
	}
	dirs, e := workSourceDirectories(root, l.Root)
	if e != nil {
		return e
	}
	for ref, mode := range dirs {
		inputs[ref] = domain.Descriptor{Type: "directory", Mode: mode}
	}
	formal := false
	for ref := range inputs {
		inWork := strings.HasPrefix(ref, l.Root+"/")
		if inputs[ref].Type != "file" || strings.HasPrefix(ref, ".yss/") {
			continue
		}
		if !inWork && id.Native != nil {
			if managed, ok := id.Native.Managed[ref]; ok && managed.Applied == inputs[ref] {
				continue
			}
		}
		raw, e := os.ReadFile(filepath.Join(root, filepath.FromSlash(ref)))
		if e != nil {
			return e
		}
		if !inWork && !bytes.Contains(raw, []byte(l.Root+"/")) && !bytes.Contains(raw, []byte("docs/.scratch/")) {
			continue
		}
		doc, e := parseWorkDocument(raw, filepath.Ext(ref))
		if e != nil {
			return domain.Wrap("WORK_LAYOUT_SCHEMA", e)
		}
		if doc != nil {
			var closure error
			workCheckReferences(doc.value, nil, inputs, nil, ref, "docs/.scratch", "", func(code, reason string) { closure = domain.Fail(code, reason) })
			if closure != nil {
				return closure
			}
			m, _ := doc.value.(map[string]any)
			formal = formal || m["gates"] != nil || m["task_id"] != nil || m["contract_id"] != nil || m["work_unit_id"] != nil || workProtected(m) || m["status"] == "completed" || m["result"] == "completed"
		}
		if strings.EqualFold(filepath.Ext(ref), ".md") && !workProtectedPath(ref) {
			if e := workCurrentMarkdown(raw, ref, inputs); e != nil {
				return e
			}
		}
	}
	// Unapproved Markdown notes have no lifecycle claims to verify. A present
	// formal asset always invokes the complete applicable governance validators;
	// missing validator assets remain a capability blocker, never a skip.
	if !formal {
		return nil
	}
	// Complete governance selects and validates applicable existing assets only.
	result, e := governance.RunContext(ctx, "project-ci", "check", root, map[string]string{"scope": "full"})
	if e != nil {
		report, ok := result.(*governance.SemanticReport)
		if !ok {
			return e
		}
		details, _ := json.Marshal(report.Diagnostics)
		return domain.Fail("WORK_LAYOUT_VERIFY", strings.ReplaceAll(string(details), root, "<candidate>"))
	}
	return e
}

func applyWorkLayout(ctx context.Context, p *Plan) (transaction.Result, error) {
	if p.WorkLayout == nil || p.Command != "migrate" || p.SchemaVersion != 2 || p.Digest != planDigest(p) {
		return transaction.Result{}, domain.Fail("PLAN", "目录迁移计划无效")
	}
	if !p.ReadyToApply || len(p.Blockers) > 0 {
		return transaction.Result{}, domain.Fail("CONFLICT", "目录迁移仍有未关闭阻断")
	}
	fresh, e := BuildWorkLayoutWithOptions(p.Root, p.Profile, PlanningOptions{ResolvedFrom: p.ResolvedFrom, Resolutions: p.Resolutions})
	if e != nil {
		return transaction.Result{}, e
	}
	if fresh.Digest != p.Digest {
		return transaction.Result{}, domain.Fail("INPUT_DRIFT", "目录迁移输入、工具或范围变化；重新生成计划")
	}
	if len(p.Changes) == 0 {
		return transaction.Result{Kind: "migrate", Status: "unchanged", Verification: "passed"}, nil
	}
	ops := []transaction.Operation{}
	for _, c := range p.Changes {
		before := c.Before
		op := transaction.Operation{Path: c.Path, Before: &before, Mode: c.After.Mode, Delete: c.After.Type == "missing", Directory: c.After.Type == "directory"}
		if !op.Delete && !op.Directory {
			op.Data, e = base64.StdEncoding.DecodeString(c.Data)
			if e != nil || safefs.Digest(op.Data) != c.After.Digest {
				return transaction.Result{}, domain.Fail("PLAN", "候选摘要无效")
			}
		}
		ops = append(ops, op)
	}
	receipt, e := jsonBytes(p.WorkLayout)
	if e != nil {
		return transaction.Result{}, e
	}
	material := transaction.Artifact{ArtifactRecord: transaction.ArtifactRecord{Path: "work-layout-mapping.json", Kind: "work-layout-mapping", Descriptor: domain.Descriptor{Type: "file", Digest: safefs.Digest(receipt), Mode: 0644}}, Data: receipt}
	return transaction.ApplyContextWithValidation(ctx, p.Root, "migrate", ops, p.Inputs, []transaction.Artifact{material}, func() error {
		for _, c := range p.Changes {
			now, e := workDescribe(p.Root, c.Path)
			if e != nil {
				return e
			}
			if now != c.After {
				return domain.Fail("VERIFY", "目录迁移后置描述不符: "+c.Path)
			}
		}
		current, e := workInventory(p.Root)
		if e != nil {
			return e
		}
		expected := map[string]domain.Descriptor{}
		for ref, d := range p.Inputs {
			if d.Type == "file" {
				expected[ref] = d
			}
		}
		for _, c := range p.Changes {
			if c.After.Type == "directory" {
				continue
			}
			if c.After.Type == "missing" {
				delete(expected, c.Path)
			} else {
				expected[c.Path] = c.After
			}
		}
		if len(current) != len(expected) {
			return domain.Fail("INPUT_DRIFT", "事务期间新增或删除输入")
		}
		for ref, d := range expected {
			if current[ref] != d {
				return domain.Fail("INPUT_DRIFT", "事务期间并发修改: "+ref)
			}
		}
		// HEAD and the exact Git index bytes remain caller-owned.
		gitNow, e := workGitIdentity(p.Root)
		if e != nil {
			return e
		}
		if gitNow != p.WorkLayout.GitIdentity {
			return domain.Fail("INPUT_DRIFT", "事务期间 Git HEAD/index 变化")
		}
		return verifyWorkLayoutCurrent(ctx, p.Root)
	})
}
