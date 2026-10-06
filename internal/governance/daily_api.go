package governance

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
)

const dailyWireProfileRef = ".agents/skills/yss-dto/references/openapi-wire-profile.yaml"

var dailyHTTPMethods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}
var dailyExactVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.+-]+)?$`)
var dailyRootDeclaration = regexp.MustCompile(`(?m)^\s*(?:openapi|swagger)\s*:|["'](?:openapi|swagger)["']\s*:`)

type dailyOAS struct {
	d        *dailySession
	baseline bool
	docs     map[string]map[string]any
	digests  map[string]string
}

func (o *dailyOAS) doc(ref string) (map[string]any, error) {
	if m := o.docs[ref]; m != nil {
		return m, nil
	}
	if err := safefs.ValidateRef(ref); err != nil {
		return nil, err
	}
	var b []byte
	var err error
	if o.baseline {
		b, err = o.d.s.git(o.d.repo, "show", o.d.base+":"+ref)
	} else {
		b, err = o.d.s.externalViews[o.d.repo].read(ref)
	}
	if err != nil {
		return nil, err
	}
	v, err := schema.Parse(b)
	if err != nil {
		return nil, err
	}
	m, ok := object(v)
	if !ok {
		return nil, fmt.Errorf("OAS/ref document must be an object")
	}
	o.docs[ref] = m
	o.digests[ref] = "sha256:" + safefs.Digest(b)
	return m, nil
}
func (o *dailyOAS) closure(ref string, v any) (any, error) {
	closure := map[string]any{}
	active := map[string]bool{}
	nodes := 0
	var visit func(string, any) (any, error)
	visit = func(file string, value any) (any, error) {
		nodes++
		if nodes > 100000 {
			return nil, fmt.Errorf("引用闭包超过受支持范围")
		}
		switch x := value.(type) {
		case map[string]any:
			if x["$dynamicRef"] != nil || x["$dynamicAnchor"] != nil || x["$id"] != nil {
				return nil, fmt.Errorf("动态或重新定位 JSON Schema 引用尚未支持")
			}
			out := map[string]any{}
			for k, child := range x {
				if k == "$ref" {
					raw, ok := child.(string)
					if !ok {
						return nil, fmt.Errorf("$ref 必须为字符串")
					}
					u, err := url.Parse(raw)
					if err != nil || u.IsAbs() || u.Host != "" || u.RawQuery != "" || strings.Contains(raw, "\\") {
						return nil, fmt.Errorf("外部或复杂引用需正式治理")
					}
					target := file
					if u.Path != "" {
						target = path.Clean(path.Join(path.Dir(file), u.Path))
					}
					if err = safefs.ValidateRef(target); err != nil {
						return nil, err
					}
					fragment := u.Fragment
					if fragment != "" && !strings.HasPrefix(fragment, "/") {
						return nil, fmt.Errorf("仅支持 JSON Pointer 引用")
					}
					key := target + "#" + fragment
					out[k] = key
					if !active[key] {
						active[key] = true
						doc, err := o.doc(target)
						if err != nil {
							return nil, err
						}
						resolved, err := dailyPointer(doc, fragment)
						if err != nil {
							return nil, err
						}
						normal, err := visit(target, resolved)
						if err != nil {
							return nil, err
						}
						closure[key] = normal
					}
					continue
				}
				normal, err := visit(file, child)
				if err != nil {
					return nil, err
				}
				out[k] = normal
			}
			return out, nil
		case []any:
			out := []any{}
			for _, child := range x {
				normal, err := visit(file, child)
				if err != nil {
					return nil, err
				}
				out = append(out, normal)
			}
			return out, nil
		default:
			return x, nil
		}
	}
	normal, err := visit(ref, v)
	if err != nil {
		return nil, err
	}
	return map[string]any{"value": normal, "reachable_refs": closure}, nil
}
func dailyPointer(root any, fragment string) (any, error) {
	v := root
	if fragment == "" {
		return v, nil
	}
	for _, raw := range strings.Split(strings.TrimPrefix(fragment, "/"), "/") {
		part := strings.ReplaceAll(strings.ReplaceAll(raw, "~1", "/"), "~0", "~")
		switch x := v.(type) {
		case map[string]any:
			var ok bool
			v, ok = x[part]
			if !ok {
				return nil, fmt.Errorf("$ref 目标不存在")
			}
		case []any:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(x) {
				return nil, fmt.Errorf("$ref 数组指针无效")
			}
			v = x[index]
		default:
			return nil, fmt.Errorf("$ref 目标不可解析")
		}
	}
	return v, nil
}

// Discover OAS roots from current files and changed baseline files. A caller's
// mode:none or selected root cannot conceal another affected reference graph.
func (d *dailySession) checkUncoveredAPI(action, declared string) error {
	changed := map[string]bool{}
	for _, ref := range d.report.ChangedFiles {
		changed[ref] = true
	}
	if len(changed) == 0 {
		return nil
	}
	files, err := d.s.git(d.repo, "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	if err != nil {
		return err
	}
	current := &dailyOAS{d: d, docs: map[string]map[string]any{}, digests: map[string]string{}}
	old := &dailyOAS{d: d, baseline: true, docs: map[string]map[string]any{}, digests: map[string]string{}}
	roots := map[string]bool{}
	for _, ref := range strings.Split(string(files), "\x00") {
		if ref == "" {
			continue
		}
		b, e := d.s.externalViews[d.repo].read(ref)
		if e != nil {
			continue
		}
		if !dailyRootDeclaration.Match(b) {
			continue
		}
		v, e := schema.Parse(b)
		if e != nil {
			if changed[ref] {
				return d.govern(action, "潜在 API 根变化不可解析")
			}
			continue
		}
		m, _ := object(v)
		if m["openapi"] != nil || m["swagger"] != nil {
			roots[ref] = true
		}
	}
	for ref := range changed {
		tree, e := d.s.git(d.repo, "ls-tree", d.base, "--", ref)
		if e != nil {
			return e
		}
		if len(tree) == 0 {
			continue
		}
		m, e := old.doc(ref)
		if e != nil {
			continue
		}
		if m["openapi"] != nil || m["swagger"] != nil {
			roots[ref] = true
		}
	}
	for root := range roots {
		if root == declared {
			continue
		}
		if changed[root] {
			return d.govern(action, "实际变更包含未声明比较覆盖的 API 根: "+root)
		}
		m, e := current.doc(root)
		if e != nil {
			return d.govern(action, "API 根或 refs 影响无法证明")
		}
		if _, e = current.closure(root, m); e != nil {
			return d.govern(action, "API refs 影响无法证明: "+e.Error())
		}
		for ref := range current.digests {
			if changed[ref] {
				return d.govern(action, "实际变更命中未声明 API 引用闭包: "+ref)
			}
		}
	}
	return nil
}
func dailyStructured(ref string) bool {
	return strings.HasSuffix(ref, ".yaml") || strings.HasSuffix(ref, ".yml") || strings.HasSuffix(ref, ".json")
}
func (d *dailySession) checkAPI(action string) error {
	api, ok := object(d.record["api"])
	if !ok {
		return d.info("必须明确 API 影响")
	}
	mode := text(api["mode"])
	declaredRoot := ""
	if mode == "compatible-additive" {
		candidate, _ := object(api["candidate"])
		declaredRoot = text(candidate["ref"])
	}
	if err := d.checkUncoveredAPI(action, declaredRoot); err != nil {
		return err
	}
	if d.report.DeliveryPath == "governed" {
		return nil
	}
	if mode == "none" {
		if strings.TrimSpace(text(api["reason"])) == "" {
			return d.info("无 API 影响需登记依据")
		}
		return nil
	}
	if mode != "compatible-additive" {
		return d.govern(action, "API 影响未知或不支持日常兼容校验")
	}
	baseline, ok := object(api["baseline"])
	candidate, cok := object(api["candidate"])
	ref := text(candidate["ref"])
	if !ok || !cok || ref == "" || text(baseline["ref"]) != ref {
		return d.info("API baseline/candidate 必须引用同一实现仓内契约")
	}
	old := &dailyOAS{d: d, baseline: true, docs: map[string]map[string]any{}, digests: map[string]string{}}
	current := &dailyOAS{d: d, docs: map[string]map[string]any{}, digests: map[string]string{}}
	a, err := old.doc(ref)
	if err != nil {
		return d.govern(action, "API 基线或引用闭包不可读取")
	}
	b, err := current.doc(ref)
	if err != nil {
		return d.govern(action, "API 候选 YAML 或引用闭包不可解析")
	}
	if !regexp.MustCompile(`^3[.]1[.][0-9]+$`).MatchString(text(a["openapi"])) || !regexp.MustCompile(`^3[.]1[.][0-9]+$`).MatchString(text(b["openapi"])) {
		return d.govern(action, "日常 API 仅支持 OAS 3.1")
	}
	if text(baseline["digest"]) != old.digests[ref] || text(candidate["digest"]) != current.digests[ref] {
		return d.rejected("API baseline/candidate 原字节摘要陈旧")
	}
	globalA := map[string]any{}
	globalB := map[string]any{}
	for k, v := range a {
		if k != "paths" && k != "components" {
			globalA[k] = v
		}
	}
	for k, v := range b {
		if k != "paths" && k != "components" {
			globalB[k] = v
		}
	}
	ca, err := old.closure(ref, globalA)
	if err != nil {
		return d.govern(action, err.Error())
	}
	cb, err := current.closure(ref, globalB)
	if err != nil {
		return d.govern(action, err.Error())
	}
	if !apEqual(ca, cb) {
		return d.govern(action, "API 全局或继承契约变化需要正式治理")
	}
	// Security scheme names and some OAS links are implicit references. Keep
	// all existing component entries stable rather than overlook such edges;
	// adding new component entries remains supported.
	componentsA, _ := object(a["components"])
	componentsB, _ := object(b["components"])
	for category, oldValue := range componentsA {
		oldEntries, valid := object(oldValue)
		newEntries, nextValid := object(componentsB[category])
		if !valid || !nextValid {
			return d.govern(action, "旧 API components 结构变化")
		}
		for name, oldEntry := range oldEntries {
			newEntry, found := newEntries[name]
			if !found {
				return d.govern(action, "旧 API component 被删除")
			}
			left, e := old.closure(ref, oldEntry)
			if e != nil {
				return d.govern(action, e.Error())
			}
			right, e := current.closure(ref, newEntry)
			if e != nil {
				return d.govern(action, e.Error())
			}
			if !apEqual(left, right) {
				return d.govern(action, "旧 API component 或引用闭包变化")
			}
		}
	}
	pathsA, aok := object(a["paths"])
	pathsB, bok := object(b["paths"])
	if !aok || !bok {
		return d.govern(action, "OAS paths 结构不可证明")
	}
	oldOps := map[string]bool{}
	newOps := []map[string]any{}
	operationIDs := map[string]bool{}
	for p, pv := range pathsA {
		item, ok := object(pv)
		if !ok || item["$ref"] != nil {
			return d.govern(action, "Path Item 引用尚未支持日常比较")
		}
		next, ok := object(pathsB[p])
		if !ok {
			return d.govern(action, "旧 API path 被删除")
		}
		inherited := map[string]any{}
		nextInherited := map[string]any{}
		for k, v := range item {
			if !semHas(dailyHTTPMethods, k) {
				inherited[k] = v
			}
		}
		for k, v := range next {
			if !semHas(dailyHTTPMethods, k) {
				nextInherited[k] = v
			}
		}
		for _, method := range dailyHTTPMethods {
			operation, present := item[method]
			if !present {
				continue
			}
			oldOps[p+"\x00"+method] = true
			newOperation, present := next[method]
			if !present {
				return d.govern(action, "旧 API operation 被删除")
			}
			oa, err := old.closure(ref, map[string]any{"operation": operation, "path_item": inherited})
			if err != nil {
				return d.govern(action, err.Error())
			}
			ob, err := current.closure(ref, map[string]any{"operation": newOperation, "path_item": nextInherited})
			if err != nil {
				return d.govern(action, err.Error())
			}
			if !apEqual(oa, ob) {
				return d.govern(action, "旧 operation 或 reachable $ref closure 有变化")
			}
		}
	}
	for p, pv := range pathsB {
		item, ok := object(pv)
		if !ok || item["$ref"] != nil {
			return d.govern(action, "Path Item 结构或引用尚未支持")
		}
		for _, method := range dailyHTTPMethods {
			value, present := item[method]
			if !present {
				continue
			}
			operation, ok := object(value)
			id := text(operation["operationId"])
			if !ok || id == "" || operationIDs[id] {
				return d.govern(action, "operationId 缺失或不唯一")
			}
			operationIDs[id] = true
			inherited := map[string]any{}
			for key, child := range item {
				if !semHas(dailyHTTPMethods, key) {
					inherited[key] = child
				}
			}
			if _, err = current.closure(ref, map[string]any{"operation": value, "path_item": inherited}); err != nil {
				return d.govern(action, err.Error())
			}
			if !oldOps[p+"\x00"+method] {
				newOps = append(newOps, map[string]any{"path": p, "method": method, "operation_id": id})
			}
		}
	}
	if len(newOps) == 0 {
		return d.govern(action, "兼容 API 路线须有新增独立 operation")
	}
	sort.Slice(newOps, func(i, j int) bool {
		return text(newOps[i]["path"])+text(newOps[i]["method"]) < text(newOps[j]["path"])+text(newOps[j]["method"])
	})
	declared, ok := api["new_operations"].([]any)
	if !ok || len(declared) != len(newOps) {
		return d.rejected("新增 API operation 清单与实际契约不符")
	}
	declaredRows := []map[string]any{}
	for _, x := range declared {
		m, ok := object(x)
		if !ok {
			return d.info("new_operations 必须为对象集合")
		}
		declaredRows = append(declaredRows, m)
	}
	sort.Slice(declaredRows, func(i, j int) bool {
		return text(declaredRows[i]["path"])+text(declaredRows[i]["method"]) < text(declaredRows[j]["path"])+text(declaredRows[j]["method"])
	})
	if !apEqual(newOps, declaredRows) {
		return d.rejected("新增 API operation 清单与实际契约不符")
	}
	if err = d.lockedTools(api); err != nil {
		return err
	}
	for file, digest := range old.digests {
		d.basis["api-baseline:"+file] = digest
	}
	for file, digest := range current.digests {
		d.basis["api-current:"+file] = digest
	}
	ruleRows, valid := api["rules"].([]any)
	if !valid || len(ruleRows) == 0 {
		return d.info("API 证据需要明确的适用规则引用")
	}
	rules := map[string]string{}
	for _, value := range ruleRows {
		row, ok := object(value)
		if !ok || text(row["digest"]) == "" {
			return d.info("API 规则必须包含原字节摘要")
		}
		if _, err := d.basisRef(text(row["ref"]), text(row["digest"])); err != nil {
			return err
		}
		rules[text(row["ref"])] = text(row["digest"])
	}
	if rules[dailyWireProfileRef] == "" {
		return d.info("API rules 必须绑定 canonical YSS DTO wire profile")
	}
	profileRaw, err := d.recordRef(dailyWireProfileRef)
	if err != nil {
		return err
	}
	profileValue, err := schema.Parse(profileRaw)
	if err != nil {
		return d.info("YSS wire profile 不可解析")
	}
	profile, _ := object(profileValue)
	version, valid := integer(profile["schema_version"])
	if !valid || version != 1 || text(profile["kind"]) != "yss-dto-openapi-wire-profile" {
		return d.s.unavailable("UNPORTED", "YSS wire profile 版本或身份不支持")
	}
	wire, _ := object(api["wire"])
	d.wireApplicable = dailyHasYSSWire(b) || text(wire["applicability"]) == "applicable"
	for _, doc := range current.docs {
		if dailyHasYSSWire(doc) {
			d.wireApplicable = true
		}
	}
	var mapperBasis any
	if d.wireApplicable {
		mapperBasis, err = d.wireBasis(api)
		if err != nil {
			return err
		}
	}
	contractPayload := map[string]any{"baseline": old.digests, "candidate": current.digests, "rules": rules, "tools": api["tools"], "compatibility_policy": d.policy["compatible_api"]}
	if mapperBasis != nil {
		contractPayload["mapper"] = mapperBasis
	}
	contractRaw, _ := json.Marshal(contractPayload)
	d.report.APIContractDigest = "sha256:" + safefs.Digest(contractRaw)
	return nil
}
func (d *dailySession) lockedTools(api map[string]any) error {
	rows, ok := api["tools"].([]any)
	if !ok || len(rows) == 0 {
		return d.info("API 校验需要锁定的工具与版本")
	}
	seen := map[string]bool{}
	for _, v := range rows {
		m, ok := object(v)
		name, version := text(m["name"]), text(m["version"])
		if !ok || name != "@redocly/cli" || !dailyExactVersion.MatchString(version) || seen[name] {
			return d.info("API 工具需要唯一名称及精确版本")
		}
		seen[name] = true
		b, err := d.basisRef(text(m["lock_ref"]), text(m["digest"]))
		if err != nil {
			return err
		}
		parsed, err := schema.Parse(b)
		if err != nil || !dailyToolLocked(parsed, name, version) {
			return d.info("锁文件未证明工具精确版本: " + name)
		}
	}
	return nil
}
func dailyToolLocked(v any, name, version string) bool {
	root, ok := object(v)
	if !ok {
		return false
	}
	for _, field := range []string{"packages", "dependencies", "snapshots"} {
		rows, _ := object(root[field])
		for key, child := range rows {
			m, _ := object(child)
			if (key == name || strings.HasSuffix(key, "node_modules/"+name)) && text(m["version"]) == version {
				return true
			}
			if key == name+"@"+version || strings.HasPrefix(key, name+"@"+version+"(") {
				return true
			}
		}
	}
	return false
}
func dailyHasYSSWire(v any) bool {
	switch x := v.(type) {
	case map[string]any:
		if x["x-yss-response-wrapper"] != nil || x["x-yss-page-query"] != nil {
			return true
		}
		props, _ := object(x["properties"])
		if props["code"] != nil && (props["data"] != nil || props["dataType"] != nil) {
			return true
		}
		for key, child := range x {
			if key == "YssResultMeta" || key == "SingleResult" || key == "MultiResult" || key == "PageResult" || key == "PageQuery" || dailyHasYSSWire(child) {
				return true
			}
		}
	case []any:
		for _, child := range x {
			if dailyHasYSSWire(child) {
				return true
			}
		}
	case string:
		for _, name := range []string{"YssResultMeta", "SingleResult", "MultiResult", "PageResult", "PageQuery"} {
			if strings.Contains(x, name) {
				return true
			}
		}
	}
	return false
}
func (d *dailySession) verifyAPIEvidence(api map[string]any, actor string) error {
	tools, _ := api["tools"].([]any)
	locked := ""
	for _, v := range tools {
		m, _ := object(v)
		if text(m["name"]) == "@redocly/cli" {
			locked = text(m["version"])
		}
	}
	lint, ok := object(api["lint"])
	argv, valid := dailyStrings(lint["argv"])
	exit, exitValid := integer(lint["exit_code"])
	candidate, _ := object(api["candidate"])
	if !ok || !valid || len(argv) < 5 || !exitValid || text(lint["tool"]) != "@redocly/cli" || text(lint["version"]) != locked || !equalStrings(argv[:4], []string{"pnpm", "exec", "redocly", "lint"}) || !semHas(argv[4:], text(candidate["ref"])) {
		return d.info("API lint 需要锁定 Redocly 及实际 pnpm exec redocly lint <OAS> 命令")
	}
	if exit != 0 {
		return d.rejected("Redocly lint 未通过")
	}
	if text(lint["api_digest"]) != d.report.APIContractDigest {
		return d.rejected("Redocly lint 未绑定当前 API 输入")
	}
	if err := d.executionLog(lint, lint["argv"], d.repo, "api_digest", d.report.APIContractDigest); err != nil {
		return err
	}
	compatibility, ok := object(api["compatibility"])
	if !ok || text(compatibility["tool"]) != "yss-native-conservative" || text(compatibility["api_digest"]) != d.report.APIContractDigest {
		return d.rejected("兼容证据必须绑定当前原生只读 operation/ref closure 比较")
	}
	wire, ok := object(api["wire"])
	if !ok {
		return d.info("缺少 DTO wire 适用性判断")
	}
	app := text(wire["applicability"])
	if app == "not-applicable" {
		if d.wireApplicable {
			return d.rejected("候选包含 YSS wrapper，不能声称 wire 不适用")
		}
		if strings.TrimSpace(text(wire["reason"])) == "" {
			return d.info("wire 不适用需原因")
		}
	} else if app == "applicable" {
		if _, err := d.wireBasis(api); err != nil {
			return err
		}
		if err := d.verifyTargetWire(api); err != nil {
			return err
		}
	} else {
		return d.info("wire 适用性必须明确")
	}
	if err := d.verifyBoundReview(api["review"], actor, "api_digest", d.report.APIContractDigest); err != nil {
		return err
	}
	freeze, ok := object(api["freeze"])
	if !ok || text(freeze["digest"]) != d.report.APIContractDigest {
		return d.rejected("API Freeze 未绑定当前 OAS、refs、适用规则与工具锁")
	}
	return d.verifyTests(api["contract_tests"], false)
}
