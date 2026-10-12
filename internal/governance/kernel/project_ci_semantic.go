package kernel

import (
	"github.com/iloveZzz/yss-cli/internal/schema"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var ciStableID = regexp.MustCompile(`^(gate|artifact|work-unit|stage|evidence|check|skill|runtime|role|module|slice|test-seam|rule|scenario|route|operation|business-ticket|context)\.[a-z0-9][a-z0-9.-]*$`)

var CiURI = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)

var CiCheckpointName = regexp.MustCompile(`(^|/)checkpoint\.(yaml|yml|json)$`)

var CiCommit = regexp.MustCompile(`^[a-fA-F0-9]{40}$`)

var ciCapturedSource = regexp.MustCompile(`^docs/(?:handoffs|backend-deliveries|spec-baselines)/[^/]+/v[1-9][0-9]*/package/`)

func ciFileRef(v any) bool {
	r, ok := v.(string)
	return ok && !CiURI.MatchString(r) && !ciStableID.MatchString(r) && strings.ContainsAny(r, "./")
}

func CiReferences(value any) []string {
	found := map[string]bool{}
	skip := map[string]bool{}
	for _, k := range []string{"term_ref", "term_refs", "skill_refs", "principal_ref", "drafter_principal_ref", "requester_ref", "responder_ref", "runtime_instance_ref", "original_ref", "owner_ref"} {
		skip[k] = true
	}
	var visit func(any)
	visit = func(v any) {
		if m, ok := Object(v); ok {
			for k, item := range m {
				if skip[k] {
					continue
				}
				if (k == "ref" || strings.HasSuffix(k, "_ref")) && ciFileRef(item) {
					found[Text(item)] = true
				}
				if strings.HasSuffix(k, "_refs") || k == "inputs" {
					for _, row := range SemList(item) {
						if ciFileRef(row) {
							found[Text(row)] = true
						}
					}
				}
				visit(item)
			}
		} else {
			for _, row := range SemList(v) {
				visit(row)
			}
		}
	}
	visit(value)
	out := []string{}
	for r := range found {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

func CiClaimed(v map[string]any) bool {
	if v == nil {
		return false
	}
	if SemHas([]string{"approved", "completed", "verified", "implemented", "accepted", "ready-for-agent", "running", "paused-human-gate"}, Text(v["status"])) || SemHas([]string{"active", "paused", "failed", "resolved"}, Text(v["workflow_status"])) || SemHas([]string{"completed", "verified"}, Text(v["result"])) || v["decision"] == "approved" {
		return true
	}
	for _, bucket := range []string{"gates", "artifacts"} {
		for _, row := range SemMap(v[bucket]) {
			if SemMap(row)["status"] == "approved" {
				return true
			}
		}
	}
	return false
}

func CiParse(bytes []byte, ref string) (map[string]any, error) {
	if strings.HasSuffix(ref, ".md") {
		if !strings.HasPrefix(string(bytes), "---\n") && !strings.HasPrefix(string(bytes), "---\r\n") {
			return nil, nil
		}
		var err error
		bytes, _, err = Frontmatter(bytes)
		if err != nil {
			return nil, err
		}
	}
	value, err := schema.Parse(bytes)
	if err != nil {
		return nil, err
	}
	m, _ := Object(value)
	return m, nil
}

// Discovery covers the legacy owner search boundary independently of --checkpoint
// and configured CI roots. An out-of-scope second owner still makes approval ambiguous.
func (s *SemanticSession) CiApprovalOwners() (map[string][]string, error) {
	owners := map[string][]string{}
	refs := []string{}
	layout, err := ViewWorkLayout(s.V)
	if err != nil {
		return nil, err
	}
	for _, root := range UniqueStrings(append(append([]string{}, layout.ScanRoots...), "docs", ".yss")) {
		files, err := s.Scan(root)
		if err != nil {
			return nil, err
		}
		refs = append(refs, files...)
	}
	entries, err := os.ReadDir(s.Root)
	if err != nil {
		return nil, s.Unavailable("INPUT", err.Error())
	}
	// contextContract's inventory observes root membership and is rechecked by finish.
	for _, entry := range entries {
		if !entry.IsDir() && SemHas([]string{".json", ".yaml", ".yml"}, filepath.Ext(entry.Name())) {
			refs = append(refs, entry.Name())
		}
	}
	for _, ref := range UniqueStrings(refs) {
		if ciCapturedSource.MatchString(ref) {
			continue
		}
		if !SemHas([]string{".json", ".yaml", ".yml"}, filepath.Ext(ref)) {
			continue
		}
		bytes, err := s.Bytes(ref)
		if err != nil {
			return nil, err
		}
		value, err := CiParse(bytes, ref)
		if err != nil || value["gate_id"] != nil || value["gates"] == nil {
			continue
		}
		for _, bucket := range []string{"gates", "checks"} {
			for _, row := range SemMap(value[bucket]) {
				approval := Text(SemMap(row)["approval_ref"])
				if approval != "" {
					owners[approval] = append(owners[approval], ref)
				}
			}
		}
	}
	for ref, rows := range owners {
		owners[ref] = UniqueStrings(rows)
	}
	return owners, nil
}

func (s *SemanticSession) CiBaseProtection(base string, roots []string, values map[string]map[string]any, checkpoints []string) error {
	if !CiCommit.MatchString(base) {
		return s.Unavailable("ARGUMENT", "--base 必须为完整40位 commit")
	}
	if _, err := s.Git(s.Root, "cat-file", "-e", base+"^{commit}"); err != nil {
		return err
	}
	bytes, err := s.Git(s.Root, "ls-tree", "-r", "--name-only", "-z", base)
	if err != nil {
		return err
	}
	oldFiles := strings.Split(strings.TrimSuffix(string(bytes), "\x00"), "\x00")
	known := map[string]bool{}
	for _, r := range oldFiles {
		known[r] = true
	}
	oldValues := map[string]map[string]any{}
	read := func(ref string) (map[string]any, error) {
		if m, ok := oldValues[ref]; ok {
			return m, nil
		}
		b, e := s.Git(s.Root, "show", base+":"+ref)
		if e != nil {
			return nil, e
		}
		m, e := CiParse(b, ref)
		if e != nil {
			return nil, s.Unavailable("INPUT", e.Error())
		}
		oldValues[ref] = m
		return m, nil
	}
	oldTracker, err := read(".template-spec/agents/issue-tracker.md")
	if err != nil {
		return err
	}
	oldRoots := []string{strings.TrimSuffix(Text(SemMap(oldTracker["tracker"])["root"]), "/")}
	if known[CiConfigRef] {
		config, e := read(CiConfigRef)
		if e != nil {
			return e
		}
		oldRoots = append(oldRoots, SemStrings(config["additional_paths"])...)
	}
	for _, r := range oldRoots {
		covered := false
		for _, now := range roots {
			if r == now || strings.HasPrefix(r, now+"/") {
				covered = true
			}
		}
		if !covered {
			return s.Reject("CI_SCOPE_REDUCED", "相对基线缩小检查范围: "+r)
		}
	}
	queue := []string{}
	for _, ref := range oldFiles {
		inside := false
		for _, r := range oldRoots {
			if ref == r || strings.HasPrefix(ref, r+"/") {
				inside = true
			}
		}
		if !inside || !SemHas([]string{".md", ".yaml", ".yml", ".json"}, filepath.Ext(ref)) {
			continue
		}
		old, e := read(ref)
		if e != nil {
			return e
		}
		if CiClaimed(old) {
			queue = append(queue, ref)
		}
		cp := CiCheckpointName.MatchString(ref) || old["repository_mode"] != nil && old["stage"] != nil && old["gates"] != nil
		if cp {
			exists, e := s.Exists(ref)
			if e != nil {
				return e
			}
			if !exists {
				return s.Reject("CHECKPOINT_DELETED", "基线 checkpoint 已删除: "+ref)
			}
			if !SemHas(checkpoints, ref) {
				return s.Reject("CHECKPOINT_UNASSESSED", "基线 checkpoint 不再被识别或检查: "+ref)
			}
			now := values[ref]
			for id, state := range SemMap(old["gates"]) {
				if SemMap(state)["status"] == "approved" && SemMap(now["gates"])[id] == nil {
					return s.Reject("APPROVAL_CLAIM_REMOVED", "基线批准被移除: "+id)
				}
			}
		}
	}
	seen := map[string]bool{}
	for len(queue) > 0 {
		ref := queue[0]
		queue = queue[1:]
		if seen[ref] {
			continue
		}
		seen[ref] = true
		exists, e := s.Exists(ref)
		if e != nil {
			return e
		}
		if !exists {
			return s.Reject("APPROVED_EVIDENCE_DELETED", "基线批准依据已删除: "+ref)
		}
		if known[ref] && SemHas([]string{".md", ".yaml", ".yml", ".json"}, filepath.Ext(ref)) {
			old, e := read(ref)
			if e != nil {
				return e
			}
			for _, dep := range CiReferences(old) {
				if known[dep] {
					queue = append(queue, dep)
				}
			}
		}
	}
	return nil
}
