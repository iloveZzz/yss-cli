package reading

import (
	"context"
	"fmt"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Reading keeps byte and parse reuse local; finishInputs still reobserves inputs.
type Sources struct {
	ScanContext      func(string) error                                               `json:"-" yaml:"-"`
	ValidateRegistry func(map[string]any) error                                       `json:"-" yaml:"-"`
	LifecycleState   func(map[string]any, map[string]any) (map[string]any, error)     `json:"-" yaml:"-"`
	Copy             func(map[string]any) map[string]any                              `json:"-" yaml:"-"`
	Same             func(any, any) bool                                              `json:"-" yaml:"-"`
	Plan             func(*kernel.SemanticSession, string) ([]PlanEntry, bool, error) `json:"-" yaml:"-"`
}
type PlanEntry struct {
	ID, Kind string
	Fields   map[string]string
}

type readingSession struct {
	sources Sources
	s       *kernel.SemanticSession
	docs    map[string]map[string]any
}

func (r *readingSession) bytes(ref string) ([]byte, error) {
	if err := r.s.Guard(); err != nil {
		return nil, err
	}
	p, err := safefs.Path(r.s.Root, ref)
	if err != nil {
		return nil, err
	}
	ref = filepath.ToSlash(filepath.Clean(ref))
	if b, ok := r.s.V.Overlay[ref]; ok {
		return b, nil
	}
	before, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, domain.Fail("INPUT", "输入必须为普通文件: "+ref)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	after, err := os.Lstat(p)
	if err != nil || !kernel.SameObservedIdentity(before, after) {
		return nil, domain.Fail("INPUT_DRIFT", "读取期间输入变化: "+ref)
	}
	r.s.V.Observed[ref] = domain.Descriptor{Type: "file", Digest: safefs.Digest(b), Mode: uint32(before.Mode().Perm())}
	if r.s.V.Identities == nil {
		r.s.V.Identities = map[string]os.FileInfo{}
	}
	r.s.V.Identities[ref] = before
	r.s.V.Overlay[ref] = b
	return b, nil
}

func (r *readingSession) doc(ref string) (map[string]any, error) {
	if strings.HasSuffix(ref, "/orchestration-contract.yaml") {
		if ref == schema.OrchestrationRef {
			var err error
			ref, err = r.s.OrchestrationReference()
			if err != nil {
				return nil, err
			}
		}
		value, _, _, err := schema.LoadOrchestration(r.bytes, ref)
		return value, err
	}
	b, err := r.bytes(ref)
	if err != nil {
		return nil, err
	}
	ref = filepath.ToSlash(filepath.Clean(ref))
	if m, ok := r.docs[ref]; ok {
		return m, nil
	}
	if strings.HasSuffix(ref, ".md") {
		b, _, err = kernel.Frontmatter(b)
		if err != nil {
			return nil, err
		}
	}
	v, err := schema.Parse(b)
	if err != nil {
		return nil, err
	}
	m, ok := kernel.Object(v)
	if !ok {
		return nil, domain.Fail("INPUT", "治理资产必须为对象: "+ref)
	}
	r.docs[ref] = m
	return m, nil
}

func (r *readingSession) binding(ref string, id, version any) map[string]any {
	ref = filepath.ToSlash(filepath.Clean(ref))
	return map[string]any{"ref": ref, "id": id, "version": version, "digest": "sha256:" + r.s.V.Observed[filepath.ToSlash(filepath.Clean(ref))].Digest}
}

func (r *readingSession) validate(ref string, value any) error {
	p, err := safefs.Path(r.s.Root, ref)
	if err != nil {
		return err
	}
	issues, err := schema.ValidateValueWithReader(p, value, func(file string) ([]byte, error) {
		local, e := filepath.Rel(r.s.Root, file)
		if e != nil {
			return nil, e
		}
		return r.bytes(filepath.ToSlash(local))
	})
	if err != nil {
		return err
	}
	if len(issues) > 0 {
		return domain.Fail("SCHEMA", fmt.Sprint(issues))
	}
	return nil
}

func Run(ctx context.Context, group, action, root string, args map[string]string, sources Sources) (any, error) {
	r := &readingSession{sources: sources, s: kernel.NewSemanticSession(ctx, root, args), docs: map[string]map[string]any{}}
	mode := args["view"]
	if group == "contract" {
		if mode == "" {
			mode = "review"
		}
		if mode != "review" && mode != "task" && mode != "full" {
			return nil, domain.Fail("ARGUMENT", "未知合同阅读视图")
		}
	} else if mode != "agent" {
		return nil, domain.Fail("ARGUMENT", "查询阅读视图仅支持 agent")
	}
	identity, err := r.doc("yss-project.yaml")
	if err != nil {
		return nil, err
	}
	n, ok := kernel.Integer(identity["schema_version"])
	if !ok || (n != 1 && n != 2) || (kernel.Text(identity["repository_mode"]) != "project-instance" && !(group == "context" && kernel.Text(identity["repository_mode"]) == "template-source")) {
		return nil, domain.Fail("IDENTITY", "阅读入口需要合法 schema v1/v2 工程身份")
	}
	if n == 2 {
		if err = r.s.Authorities(); err != nil {
			return nil, err
		}
	}
	var content, binding map[string]any
	scope := "selected-object-and-explicit-byte-bindings"
	switch group {
	case "contract":
		content, binding, err = r.slice(args, mode)
	case "context":
		content, binding, err = r.context(args)
	case "lifecycle":
		content, binding, err = r.lifecycle(action, args)
		scope = "registered-state-and-explicit-references"
	default:
		err = domain.Fail("ARGUMENT", "不支持阅读入口")
	}
	if err != nil {
		return nil, err
	}
	if err = r.s.FinishInputs(); err != nil {
		return nil, err
	}
	refs := []string{}
	for ref := range r.s.V.Observed {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	inputs := []any{}
	for _, ref := range refs {
		inputs = append(inputs, r.binding(ref, nil, nil))
	}
	return map[string]any{"view": mode, "kind": group, "binding": binding, "project_identity": identity, "content": content, "blockers": readingBlockers(content), "checks": map[string]any{"scope": scope, "inputs": inputs, "input_drift": false, "approval_validity": "not-checked", "repository_regression": "not-run"}, "read_only": true, "execution_allowed": false, "execution_authorization": "not-evaluated", "approval_created": false}, nil
}

func (r *readingSession) context(args map[string]string) (map[string]any, map[string]any, error) {
	id, refs := args["id"], args["term-refs"]
	if (id == "") == (refs == "") {
		return nil, nil, domain.Fail("ARGUMENT", "agent Context 必须选择 --id 或 --term-refs")
	}
	if strings.Contains(id, ",") {
		return nil, nil, domain.Fail("ARGUMENT", "--id 只选择一个术语")
	}
	if err := r.sources.ScanContext(r.s.Root); err != nil {
		return nil, nil, err
	}
	// Bind the uniqueness scan as well as the selected document's bytes.
	inventory, err := r.s.ContextFiles()
	if err != nil {
		return nil, nil, err
	}
	r.s.ContextInventory = inventory
	b, err := r.bytes("CONTEXT.md")
	if err != nil {
		return nil, nil, err
	}
	m, err := kernel.ContextContractBytes(b)
	if err != nil {
		return nil, nil, err
	}
	all := m["business_terms"].([]kernel.Term)
	if args["allowed-context-ids"] != "" {
		allowed := map[string]bool{}
		for _, v := range strings.Split(args["allowed-context-ids"], ",") {
			allowed[strings.TrimSpace(v)] = true
		}
		for _, term := range all {
			if !allowed[term.ContextID] {
				return nil, nil, domain.Fail("CONTEXT_REFERENCE", "未登记的业务责任区: "+term.ContextID)
			}
		}
	}
	selected := []kernel.Term{}
	wanted := strings.Split(kernel.First(id, refs), ",")
	seen := map[string]bool{}
	for _, v := range wanted {
		v = strings.TrimSpace(v)
		if seen[v] {
			continue
		}
		found := false
		for _, term := range all {
			if term.TermRef == v {
				selected = append(selected, term)
				found = true
				break
			}
		}
		if !found {
			return nil, nil, domain.Fail("CONTEXT_REFERENCE", "未知术语: "+v)
		}
		seen[v] = true
	}
	digest, err := kernel.TermsDigest(selected)
	if err != nil {
		return nil, nil, err
	}
	termRefs := []string{}
	for _, term := range selected {
		termRefs = append(termRefs, term.TermRef)
	}
	content := map[string]any{"terms": selected, "context_schema_version": m["context_schema_version"], "context_ref": "CONTEXT.md", "document_digest": m["document_digest"], "referenced_terms_digest": digest, "term_refs": termRefs}
	return content, r.binding("CONTEXT.md", "context", m["context_schema_version"]), nil
}

func readingBlockers(value any) []any {
	result := []any{}
	var walk func(any, string)
	walk = func(v any, p string) {
		switch x := v.(type) {
		case map[string]any:
			keys := []string{}
			for k := range x {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				path := p + "/" + k
				if k == "blockers" || k == "blocking_items" || k == "stale_inputs" || k == "blocking_findings" || k == "readiness_blockers" {
					if rows, ok := x[k].([]any); ok {
						for _, row := range rows {
							result = append(result, map[string]any{"source_pointer": path, "item": row})
						}
					} else if x[k] != nil {
						result = append(result, map[string]any{"source_pointer": path, "item": x[k]})
					}
					continue
				}
				walk(x[k], path)
			}
		case []any:
			for i, row := range x {
				walk(row, p+"/"+strconv.Itoa(i))
			}
		}
	}
	walk(value, "")
	return result
}

func (r *readingSession) lifecycle(action string, args map[string]string) (map[string]any, map[string]any, error) {
	const ref = ".template-spec/process/lifecycle-registry.yaml"
	m, err := r.doc(ref)
	if err != nil {
		return nil, nil, err
	}
	if err = r.sources.ValidateRegistry(m); err != nil {
		return nil, nil, err
	}
	if action == "query" {
		id := args["id"]
		if id == "" {
			return nil, nil, domain.Fail("ARGUMENT", "agent lifecycle query 必须指定 --id")
		}
		kind, entry, ok := kernel.FindRegistry(m, id)
		if !ok {
			return nil, nil, domain.Fail("LIFECYCLE_ID", "未登记稳定 ID: "+id)
		}
		return map[string]any{"kind": kind, "entry": entry}, r.binding(ref, id, m["schema_version"]), nil
	}
	if args["checkpoint"] == "" {
		return nil, nil, domain.Fail("ARGUMENT", "agent status 必须指定 --checkpoint")
	}
	cpRef := args["checkpoint"]
	cp, err := r.doc(cpRef)
	if err != nil {
		return nil, nil, err
	}
	n, ok := kernel.Integer(cp["schema_version"])
	if !ok || (n != 1 && n != 2) || kernel.Text(cp["repository_mode"]) != "project-instance" {
		return nil, nil, domain.Fail("LIFECYCLE", "checkpoint 身份或 schema 不支持")
	}
	content, err := r.sources.LifecycleState(m, cp)
	if err != nil {
		return nil, nil, err
	}
	registered := map[string]any{}
	for _, key := range []string{"status", "gates", "stage_tracking", "pause", "context_reconciliation", "verification", "artifacts"} {
		registered[key] = readingEvidenceRefs(cp[key], key != "artifacts")
	}
	content["registered_state"] = registered
	content["blockers"] = readingBlockers(cp)
	content["next_action"] = readingEvidenceRefs(cp["next_action"], true)
	if content["next_action"] == nil {
		content["next_action"] = map[string]any{"work_unit": cp["next_work_unit"], "authorization": "not-evaluated"}
	}
	content["evidence_refs"] = readingEvidenceIndex(cp)
	content["registry_binding"] = r.binding(ref, "lifecycle-registry", m["schema_version"])
	return content, r.binding(cpRef, cp["feature_id"], cp["schema_version"]), nil
}

// Artifact content is a registered constraint; evidence content is an embedded body.
func readingEvidenceRefs(value any, evidence bool) any {
	switch x := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, v := range x {
			if k == "body" || k == "text" || k == "stdout" || k == "stderr" || k == "markdown" || (evidence && k == "content") {
				continue
			}
			out[k] = readingEvidenceRefs(v, evidence || k == "evidence" || k == "evidence_refs")
		}
		return out
	case []any:
		out := []any{}
		for _, v := range x {
			out = append(out, readingEvidenceRefs(v, evidence))
		}
		return out
	default:
		return value
	}
}

func (r *readingSession) slice(args map[string]string, mode string) (map[string]any, map[string]any, error) {
	if args["kind"] != "slice" || args["file"] == "" {
		return nil, nil, domain.Fail("ARGUMENT", "contract view 需要 --kind slice --file")
	}
	ref := args["file"]
	doc, err := r.doc(ref)
	if err != nil {
		return nil, nil, err
	}
	raw := doc
	if wrapped, ok := kernel.Object(doc["slice_contract"]); ok {
		raw = wrapped
	}
	n, ok := kernel.Integer(raw["schema_version"])
	if !ok || (n != 2 && n != 3 && n != 4) {
		return nil, nil, domain.Fail("CONTRACT_SCHEMA", "Slice 阅读仅支持 v2/v3/v4")
	}
	if n == 4 && r.s.GovernanceVersion != 2 {
		return nil, nil, domain.Fail("CONTRACT_SCHEMA", "Slice4需要当前role-independent政策")
	}
	if n == 3 || n == 4 {
		err = r.validate(fmt.Sprintf(".template-spec/process/schemas/slice-implementation-contract-v%d.schema.json", n), raw)
	} else {
		err = r.sliceV2(raw)
	}
	if err != nil {
		return nil, nil, err
	}
	binding := r.binding(ref, raw["contract_id"], raw["contract_version"])
	sources, err := r.sliceSources(raw)
	if err != nil {
		return nil, nil, err
	}
	content := r.sources.Copy(raw)
	if mode != "task" {
		if source, ok := kernel.Object(sources["ticket"]); ok {
			b, e := r.bytes(kernel.Text(source["ref"]))
			if e != nil {
				return nil, nil, e
			}
			content["ticket_summary"] = readingTicketSummary(b)
		}
	}
	units := kernel.SemList(raw["work_units"])
	seen := map[string]bool{}
	var selected map[string]any
	for _, v := range units {
		u := kernel.SemMap(v)
		if nested, ok := kernel.Object(u["work_unit"]); ok {
			u = nested
		}
		id := kernel.Text(u["id"])
		if id == "" || seen[id] {
			return nil, nil, domain.Fail("CONTRACT_INVALID", "工作单元缺失或重复: "+id)
		}
		seen[id] = true
		if id == args["unit"] {
			selected = r.sources.Copy(u)
		}
	}
	if mode != "task" && args["unit"] != "" {
		return nil, nil, domain.Fail("ARGUMENT", "--unit 仅适用于 task")
	}
	acceptance := map[string]any{}
	if n == 3 || n == 4 {
		for id, v := range kernel.SemMap(raw["acceptance"]) {
			a := kernel.SemMap(v)
			src, ok := sources[kernel.Text(a["source"])]
			if !ok {
				return nil, nil, domain.Fail("CONTRACT_INVALID", "验收来源缺失: "+id)
			}
			source := kernel.SemMap(src)
			b, e := r.bytes(kernel.Text(source["ref"]))
			if e != nil {
				return nil, nil, e
			}
			excerpt, e := r.locate(kernel.Text(source["ref"]), b, kernel.Text(a["locator"]))
			if e != nil {
				return nil, nil, e
			}
			acceptance[id] = map[string]any{"text": excerpt, "source": a, "binding": src}
		}
	} else {
		if ticket := kernel.Text(kernel.SemMap(raw["lifecycle_refs"])["ticket"]); ticket != "" {
			b, e := r.bytes(ticket)
			if e != nil {
				return nil, nil, e
			}
			content["ticket"] = string(b)
		}
	}
	if mode == "task" {
		if args["unit"] == "" || selected == nil {
			return nil, nil, domain.Fail("CONTRACT_INVALID", "task 必须指定唯一已登记 --unit")
		}
		content["work_units"] = []any{selected}
		content["reading_stop_conditions"] = "发现 violation、drift 或 new_impacts 时停止受影响工作，保留修改与证据并回交治理；阅读视图不授予批准或执行权。"
		if n == 3 || n == 4 {
			scope := kernel.SemMap(raw["scope"])
			for _, k := range []string{"project_root", "allowed_write_paths"} {
				if selected[k] == nil {
					if k == "project_root" {
						roots := kernel.SemList(scope["project_roots"])
						if len(roots) != 1 {
							return nil, nil, domain.Fail("CONTRACT_INVALID", "多仓工作单元必须指定 project_root")
						}
						selected[k] = roots[0]
					} else {
						selected[k] = scope[k]
					}
				}
			}
			checks := map[string]any{}
			for id, v := range kernel.SemMap(raw["verification"]) {
				if kernel.SemMap(v)["required_for_all"] == true {
					checks[id] = v
				}
			}
			for _, id := range kernel.SemStrings(selected["verification_refs"]) {
				v, ok := kernel.SemMap(raw["verification"])[id]
				if !ok {
					return nil, nil, domain.Fail("CONTRACT_INVALID", "未知验证: "+id)
				}
				checks[id] = v
			}
			wanted := map[string]bool{}
			for _, id := range kernel.SemStrings(selected["acceptance_refs"]) {
				wanted[id] = true
			}
			for _, v := range checks {
				for _, id := range kernel.SemStrings(kernel.SemMap(v)["acceptance_refs"]) {
					wanted[id] = true
				}
			}
			focused := map[string]any{}
			for id := range wanted {
				v, ok := acceptance[id]
				if !ok {
					return nil, nil, domain.Fail("CONTRACT_INVALID", "未知验收: "+id)
				}
				focused[id] = v
			}
			content["acceptance"] = focused
			content["verification"] = checks
			pointer := "/acceptance"
			if doc["slice_contract"] != nil {
				pointer = "/slice_contract/acceptance"
			}
			content["full_acceptance_ref"] = map[string]any{"binding": binding, "pointer": pointer, "ids": sortedMapKeys(acceptance)}
		}
	} else if n == 3 || n == 4 {
		content["acceptance"] = acceptance
	}
	if n == 3 || n == 4 {
		content["basis"] = sources
	}
	return content, binding, nil
}

func sortedMapKeys(m map[string]any) []string {
	keys := []string{}
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func readingTicketSummary(b []byte) string {
	body := string(b)
	if strings.HasPrefix(body, "---\n") || strings.HasPrefix(body, "---\r\n") {
		_, raw, e := kernel.Frontmatter(b)
		if e == nil {
			body = string(raw)
		}
	}
	out := []string{}
	skipDepth := 0
	for _, line := range strings.Split(body, "\n") {
		trim := strings.TrimSpace(line)
		depth := len(trim) - len(strings.TrimLeft(trim, "#"))
		if depth > 0 && len(trim) > depth && trim[depth] == ' ' {
			if skipDepth > 0 && depth <= skipDepth {
				skipDepth = 0
			}
			if strings.Contains(strings.TrimSpace(trim[depth:]), "验收") {
				skipDepth = depth
			}
		}
		if skipDepth == 0 {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

func readingEvidenceIndex(value any) []any {
	out := []any{}
	var walk func(any, string)
	walk = func(v any, pointer string) {
		switch x := v.(type) {
		case map[string]any:
			for _, key := range sortedMapKeys(x) {
				if key == "evidence_refs" {
					for i, row := range kernel.SemList(x[key]) {
						if ref := kernel.Text(row); ref != "" {
							out = append(out, map[string]any{"source_pointer": pointer + "/" + key + "/" + strconv.Itoa(i), "ref": ref})
						}
					}
				}
				if key == "ref" || strings.HasSuffix(key, "_ref") {
					if ref := kernel.Text(x[key]); ref != "" {
						item := map[string]any{"source_pointer": pointer + "/" + key, "ref": ref}
						for _, field := range []string{"digest", "status", "result", "summary"} {
							if x[field] != nil {
								item[field] = x[field]
							}
						}
						out = append(out, item)
					}
				}
				walk(x[key], pointer+"/"+key)
			}
		case []any:
			for i, row := range x {
				walk(row, pointer+"/"+strconv.Itoa(i))
			}
		}
	}
	walk(value, "")
	return out
}

func (r *readingSession) sliceV2(raw map[string]any) error {
	compiler, err := r.doc(".agents/skills/yss-implementation-contract-compiler/references/compiler-contract.yaml")
	if err != nil {
		return err
	}
	for section, fields := range kernel.SemMap(compiler["slice_contract_required"]) {
		target := raw
		if section != "root" {
			target = kernel.SemMap(raw[section])
		}
		for _, field := range kernel.SemStrings(fields) {
			if _, ok := target[field]; !ok {
				return domain.Fail("CONTRACT_INVALID", "缺少 "+section+"."+field)
			}
		}
	}
	for _, section := range []string{"common", "resolution", "backend"} {
		if _, ok := kernel.SemMap(raw[section])["required_skills"].([]any); !ok {
			return domain.Fail("CONTRACT_INVALID", section+".required_skills 须为数组")
		}
	}
	if len(kernel.SemList(raw["work_units"])) == 0 {
		return domain.Fail("CONTRACT_INVALID", "缺少工作单元")
	}
	for _, v := range kernel.SemList(raw["work_units"]) {
		u := kernel.SemMap(v)
		if !r.sources.Same(u["contract_id"], raw["contract_id"]) || !r.sources.Same(u["contract_version"], raw["contract_version"]) || kernel.Text(kernel.SemMap(u["work_unit"])["primary_skill"]) == "" {
			return domain.Fail("CONTRACT_INVALID", "工作单元身份冲突")
		}
	}
	return nil
}

func (r *readingSession) sliceSources(raw map[string]any) (map[string]any, error) {
	basis := kernel.SemMap(raw["basis"])
	out := map[string]any{}
	active := map[string]bool{}
	var resolve func(string) (map[string]any, error)
	resolve = func(key string) (map[string]any, error) {
		if v, ok := kernel.Object(out[key]); ok {
			return v, nil
		}
		if active[key] {
			return nil, domain.Fail("CONTRACT_INVALID", "依据别名循环: "+key)
		}
		active[key] = true
		defer delete(active, key)
		v, ok := basis[key]
		if !ok {
			return nil, domain.Fail("CONTRACT_INVALID", "依据缺失: "+key)
		}
		if alias, ok := v.(string); ok {
			b, e := resolve(alias)
			if e == nil {
				out[key] = b
			}
			return b, e
		}
		b, ok := kernel.Object(v)
		if !ok {
			return nil, domain.Fail("CONTRACT_INVALID", "依据必须为绑定: "+key)
		}
		ref := kernel.Text(b["ref"])
		if _, e := r.bytes(ref); e != nil {
			return nil, e
		}
		if kernel.Text(b["digest"]) != "sha256:"+r.s.V.Observed[filepath.ToSlash(filepath.Clean(ref))].Digest {
			return nil, domain.Fail("STALE", "依据原字节变化: "+key)
		}
		out[key] = b
		return b, nil
	}
	for _, key := range sortedMapKeys(basis) {
		if _, err := resolve(key); err != nil {
			return nil, err
		}
	}
	// v2's named lifecycle references are explicit reading inputs, not approval checks.
	if _, ok := kernel.Integer(raw["schema_version"]); ok && kernel.ContractN(raw["schema_version"]) == 2 {
		for _, value := range kernel.SemMap(raw["lifecycle_refs"]) {
			ref := kernel.Text(value)
			if ref != "" {
				if _, err := r.bytes(ref); err != nil {
					return nil, err
				}
				out[ref] = r.binding(ref, nil, nil)
			}
		}
	}
	return out, nil
}

func (r *readingSession) locate(ref string, b []byte, locator string) (string, error) {
	if strings.HasPrefix(locator, "pointer:") {
		value, err := r.doc(ref)
		if err != nil {
			return "", err
		}
		var current any = value
		pointer := strings.TrimPrefix(locator, "pointer:")
		if pointer != "" {
			if !strings.HasPrefix(pointer, "/") {
				return "", domain.Fail("LOCATOR_INVALID", locator)
			}
			for _, key := range strings.Split(pointer[1:], "/") {
				key = strings.ReplaceAll(strings.ReplaceAll(key, "~1", "/"), "~0", "~")
				switch x := current.(type) {
				case map[string]any:
					var ok bool
					current, ok = x[key]
					if !ok {
						return "", domain.Fail("LOCATOR_INVALID", locator)
					}
				case []any:
					i, e := strconv.Atoi(key)
					if e != nil || i < 0 || i >= len(x) {
						return "", domain.Fail("LOCATOR_INVALID", locator)
					}
					current = x[i]
				default:
					return "", domain.Fail("LOCATOR_INVALID", locator)
				}
			}
		}
		return fmt.Sprint(current), nil
	}
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	if strings.HasPrefix(locator, "lines:") {
		var a, z int
		if _, e := fmt.Sscanf(locator, "lines:%d-%d", &a, &z); e != nil || a < 1 || z < a || z > len(lines) {
			return "", domain.Fail("LOCATOR_INVALID", locator)
		}
		return strings.Join(lines[a-1:z], "\n"), nil
	}
	if strings.HasPrefix(locator, "AC-") {
		entries, supported, e := r.sources.Plan(r.s, ref)
		if e != nil {
			return "", e
		}
		if supported {
			var found *PlanEntry
			for i := range entries {
				if entries[i].Kind == "AC" && entries[i].ID == locator {
					if found != nil {
						return "", domain.Fail("LOCATOR_INVALID", locator)
					}
					found = &entries[i]
				}
			}
			if found == nil {
				return "", domain.Fail("LOCATOR_INVALID", locator)
			}
			return fmt.Sprint(found.Fields), nil
		}
	}
	body := string(b)
	if strings.HasPrefix(body, "---\n") || strings.HasPrefix(body, "---\r\n") {
		_, raw, e := kernel.Frontmatter(b)
		if e != nil {
			return "", e
		}
		body = string(raw)
	}
	matches := []string{}
	if locator != "" {
		for _, line := range strings.Split(body, "\n") {
			if strings.Contains(line, locator) {
				matches = append(matches, line)
			}
		}
	}
	if len(matches) != 1 {
		return "", domain.Fail("LOCATOR_INVALID", locator)
	}
	return matches[0], nil
}
