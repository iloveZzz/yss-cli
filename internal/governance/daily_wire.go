package governance

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
)

// Target-wire evidence describes observed HTTP serialization, never Java
// getters or a successful profile-only command. Mapper inputs are part of the
// API freeze; samples/logs are completion evidence and are checked afresh.
func (d *dailySession) wireBasis(api map[string]any) (any, error) {
	w, ok := object(api["wire"])
	if !ok || text(w["applicability"]) != "applicable" {
		return nil, d.info("YSS wire 需要目标 mapper 及样本证据，不能声称不适用")
	}
	mapper, ok := object(w["mapper"])
	rows, valid := mapper["inputs"].([]any)
	if !ok || strings.TrimSpace(text(mapper["id"])) == "" || !valid || len(rows) == 0 {
		return nil, d.info("wire.mapper 需要实际 HTTP mapper 身份和配置输入")
	}
	inputs := map[string]string{}
	for _, v := range rows {
		row, ok := object(v)
		ref := text(row["ref"])
		if !ok || text(row["digest"]) == "" {
			return nil, d.info("mapper 输入需要原字节摘要")
		}
		b, e := d.s.externalViews[d.repo].read(ref)
		if e != nil {
			return nil, e
		}
		digest := "sha256:" + safefs.Digest(b)
		if digest != text(row["digest"]) {
			return nil, d.rejected("mapper 输入陈旧: " + ref)
		}
		inputs[ref] = digest
	}
	runtime, ok := object(mapper["runtime"])
	if !ok || text(runtime["digest"]) == "" {
		return nil, d.info("mapper 缺少实际运行配置及构件摘要")
	}
	raw, err := d.basisRef(text(runtime["ref"]), text(runtime["digest"]))
	if err != nil {
		return nil, err
	}
	value, err := schema.Parse(raw)
	if err != nil {
		return nil, d.info("mapper 运行配置不可解析")
	}
	facts, ok := object(value)
	artifacts, _ := object(facts["artifact_digests"])
	_, features := integer(facts["serialization_features"])
	_, modules := facts["registered_modules"].([]any)
	if !ok || text(facts["id"]) != text(mapper["id"]) || !features || !modules || len(artifacts) == 0 {
		return nil, d.info("mapper 运行配置缺少实际身份、modules、features 或构件摘要")
	}
	for _, v := range artifacts {
		digest := text(v)
		if !strings.HasPrefix(digest, "sha256:") || len(digest) != 71 {
			return nil, d.info("mapper 构件摘要非法")
		}
		for _, c := range strings.TrimPrefix(digest, "sha256:") {
			if !strings.ContainsRune("0123456789abcdef", c) {
				return nil, d.info("mapper 构件摘要非法")
			}
		}
	}
	return map[string]any{"id": mapper["id"], "inputs": inputs, "runtime": runtime["digest"]}, nil
}

// Translate the already observed local OAS closure to one offline schema
// resource. JSON Schema semantics stay with the pinned engine (including
// recursive refs); no network or files are generated.
func wireSchema(o *dailyOAS, ref, pointer string) (map[string]any, error) {
	doc, e := o.doc(ref)
	if e != nil {
		return nil, e
	}
	v, e := dailyPointer(doc, pointer)
	if e != nil {
		return nil, e
	}
	closed, e := o.closure(ref, v)
	if e != nil {
		return nil, e
	}
	c, _ := object(closed)
	refs, _ := object(c["reachable_refs"])
	keys := make([]string, 0, len(refs))
	for k := range refs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	ids := map[string]string{}
	for i, k := range keys {
		ids[k] = fmt.Sprintf("r%d", i)
	}
	var rewrite func(any) any
	rewrite = func(v any) any {
		switch x := v.(type) {
		case map[string]any:
			y := map[string]any{}
			for k, v := range x {
				if k == "$ref" {
					y[k] = "#/$defs/" + ids[text(v)]
				} else {
					y[k] = rewrite(v)
				}
			}
			return y
		case []any:
			y := make([]any, len(x))
			for i, v := range x {
				y[i] = rewrite(v)
			}
			return y
		default:
			return v
		}
	}
	defs := map[string]any{}
	for _, k := range keys {
		defs[ids[k]] = rewrite(refs[k])
	}
	return map[string]any{"$schema": "https://json-schema.org/draft/2020-12/schema", "$defs": defs, "allOf": []any{rewrite(c["value"])}}, nil
}
func wireValidate(root map[string]any, value any) error {
	raw, e := json.Marshal(root)
	if e != nil {
		return e
	}
	issues, e := schema.ValidateValueWithReader("/yss-wire/candidate.json", value, func(p string) ([]byte, error) {
		if filepath.Clean(p) != "/yss-wire/candidate.json" {
			return nil, fmt.Errorf("unobserved schema %s", p)
		}
		return raw, nil
	})
	if e != nil {
		return e
	}
	if len(issues) != 0 {
		return fmt.Errorf("sample violates candidate schema: %s %s", issues[0].Path, issues[0].Message)
	}
	return nil
}
func wireShape(root map[string]any) (map[string]any, map[string]bool, error) {
	props := map[string]any{}
	required := map[string]bool{}
	seen := map[string]bool{}
	var walk func(any) error
	walk = func(v any) error {
		m, ok := object(v)
		if !ok {
			return fmt.Errorf("wrapper schema is not an object")
		}
		for _, k := range []string{"oneOf", "anyOf", "if", "not", "unevaluatedProperties"} {
			if m[k] != nil {
				return fmt.Errorf("wrapper composition %s needs governed review", k)
			}
		}
		if r := text(m["$ref"]); r != "" {
			if !seen[r] {
				seen[r] = true
				v, e := dailyPointer(root, strings.TrimPrefix(r, "#"))
				if e != nil {
					return e
				}
				if e = walk(v); e != nil {
					return e
				}
			}
		}
		p, _ := object(m["properties"])
		for k, v := range p {
			if old, ok := props[k]; ok && !apEqual(old, v) {
				return fmt.Errorf("ambiguous wrapper property %s", k)
			}
			props[k] = v
		}
		for _, v := range dailyWireArray(m["required"]) {
			required[text(v)] = true
		}
		for _, v := range dailyWireArray(m["allOf"]) {
			if e := walk(v); e != nil {
				return e
			}
		}
		return nil
	}
	if e := walk(root); e != nil {
		return nil, nil, e
	}
	return props, required, nil
}
func wireField(rule, field map[string]any, required bool) error {
	if field == nil {
		return fmt.Errorf("field missing")
	}
	if rule["required"] == false && required {
		return fmt.Errorf("optional field is required")
	}
	if rule["items"] == "endpoint-schema" {
		items, ok := object(field["items"])
		if !ok || len(items) == 0 {
			return fmt.Errorf("array items require explicit endpoint schema")
		}
	}
	if rule["required"] == true && !required {
		return fmt.Errorf("required field is optional")
	}
	allowed, _ := dailyStrings(rule["wire_types"])
	if len(allowed) > 0 {
		types := []string{}
		if s := text(field["type"]); s != "" {
			types = []string{s}
		} else {
			types, _ = dailyStrings(field["type"])
		}
		if len(types) == 0 {
			return fmt.Errorf("field type cannot be proven")
		}
		for _, t := range types {
			if !semHas(allowed, t) {
				return fmt.Errorf("field type %s is outside wire profile", t)
			}
		}
		if rule["nullable"] == true && !semHas(types, "null") {
			return fmt.Errorf("nullable field excludes null")
		}
		if rule["nullable"] == false && semHas(types, "null") {
			return fmt.Errorf("non-null field permits null")
		}
	}
	for _, k := range []string{"format", "default", "enum"} {
		if rule[k] != nil && !apEqual(rule[k], field[k]) {
			return fmt.Errorf("field %s differs from wire profile", k)
		}
	}
	if rule["minimum"] != nil {
		a, ok := integer(rule["minimum"])
		b, valid := integer(field["minimum"])
		if !ok || !valid || b != a {
			return fmt.Errorf("field minimum weakens wire profile")
		}
	}
	return nil
}
func wireProfileShape(profile, root map[string]any, kind string) error {
	actualKind, objectOnly, identityErr := wireIdentity(root)
	if identityErr != nil {
		return identityErr
	}
	if actualKind != kind || !objectOnly {
		return fmt.Errorf("wrapper annotation/object type differs from sample: %s", kind)
	}
	props, required, e := wireShape(root)
	if e != nil {
		return e
	}
	checks := map[string]any{}
	if kind == "PageQuery" {
		pq, _ := object(profile["page_query"])
		checks, _ = object(pq["fields"])
		for _, v := range dailyWireArray(pq["forbidden_client_fields"]) {
			if props[text(v)] != nil {
				return fmt.Errorf("internal paging field exposed: %s", text(v))
			}
		}
	} else {
		common, _ := object(profile["common_response"])
		fields, _ := object(common["fields"])
		for k, v := range fields {
			checks[k] = v
		}
		wrappers, _ := object(profile["wrappers"])
		wrapper, ok := object(wrappers[kind])
		if !ok {
			return fmt.Errorf("unknown wrapper %s", kind)
		}
		fields, _ = object(wrapper["fields"])
		for k, v := range fields {
			checks[k] = v
		}
		if data := wrapper["data"]; data != nil {
			checks["data"] = data
		}
	}
	if len(checks) == 0 {
		return fmt.Errorf("wire profile has no field rules")
	}
	for name, v := range checks {
		rule, _ := object(v)
		field, _ := object(props[name])
		if e := wireField(rule, field, required[name]); e != nil {
			return fmt.Errorf("%s.%s: %w", kind, name, e)
		}
		if kind == "PageQuery" && (name == "orderBy" || name == "groupBy") {
			whitelist := 0
			for _, v := range dailyWireArray(field["enum"]) {
				if text(v) != "" {
					whitelist++
				} else if v != nil {
					return fmt.Errorf("%s whitelist contains invalid value", name)
				}
			}
			if whitelist == 0 {
				return fmt.Errorf("%s requires endpoint whitelist", name)
			}
		}
	}
	if kind == "PageQuery" {
		for _, v := range dailyWireArray(profile["page_query"].(map[string]any)["forbidden_client_fields"]) {
			if wireValidate(root, map[string]any{text(v): 0}) == nil {
				return fmt.Errorf("PageQuery accepts forbidden client field: %s", text(v))
			}
		}
	}
	if kind == "SingleResult" {
		data, _ := object(props["data"])
		test := map[string]any{"$schema": root["$schema"], "$defs": root["$defs"], "allOf": []any{data}}
		if e := wireValidate(test, nil); e != nil {
			return fmt.Errorf("SingleResult.data must allow null")
		}
	}
	return nil
}
func (d *dailySession) verifyTargetWire(api map[string]any) error {
	w, _ := object(api["wire"])
	mapper, _ := object(w["mapper"])
	argv, ok := dailyStrings(w["argv"])
	exit, valid := integer(w["exit_code"])
	if !ok || len(argv) < 2 || !valid || exit != 0 || text(w["api_digest"]) != d.report.APIContractDigest {
		return d.info("wire 需要通过且绑定当前 API 的真实测试命令")
	}
	if !wireTestCommand(argv) {
		return d.info("wire 命令必须是工程 Maven/pnpm 测试，不能用 echo/profile 校验")
	}
	registered := false
	for _, v := range dailyWireArray(d.record["commands"]) {
		c, _ := object(v)
		if apEqual(c["argv"], w["argv"]) {
			registered = true
		}
	}
	if !registered {
		return d.info("wire 测试必须是登记的工程验证命令")
	}
	if e := d.executionLog(w, w["argv"], d.repo, "api_digest", d.report.APIContractDigest); e != nil {
		return e
	}
	log, e := d.factRecord(text(w["log_ref"]))
	if e != nil {
		return e
	}
	runtime, _ := object(mapper["runtime"])
	facts, e := d.factRecord(text(runtime["ref"]))
	if e != nil {
		return e
	}
	observed := false
	for _, line := range strings.Split(text(log["stdout"]), "\n") {
		if strings.HasPrefix(line, "YSS_WIRE_MAPPER ") {
			v, err := schema.Parse([]byte(strings.TrimPrefix(line, "YSS_WIRE_MAPPER ")))
			if err == nil && apEqual(v, facts) {
				observed = true
			}
		}
	}
	if !observed {
		return d.rejected("实际测试 stdout 未证明当前 mapper 运行配置和构件摘要")
	}
	if text(log["mapper_id"]) != text(mapper["id"]) {
		return d.rejected("实际日志 mapper 身份不一致")
	}
	rows, ok := w["samples"].([]any)
	if !ok || len(rows) == 0 {
		return d.info("缺少真实 HTTP/序列化样本")
	}
	if !apEqual(log["samples"], w["samples"]) {
		return d.rejected("wire 样本与实际日志不一致")
	}
	profileRaw, e := d.recordRef(dailyWireProfileRef)
	if e != nil {
		return e
	}
	v, e := schema.Parse(profileRaw)
	if e != nil {
		return e
	}
	profile, _ := object(v)
	candidate, _ := object(api["candidate"])
	ref := text(candidate["ref"])
	o := &dailyOAS{d: d, docs: map[string]map[string]any{}, digests: map[string]string{}}
	covered := map[string]bool{}
	for _, v := range rows {
		row, ok := object(v)
		pointer := text(row["oas_pointer"])
		kind := text(row["wrapper"])
		if !ok || !strings.HasPrefix(pointer, "/") {
			return d.info("wire 样本需要候选 OAS JSON Pointer 与 wrapper")
		}
		if !wireEmitted(log, text(mapper["id"]), kind, text(row["sample_digest"])) {
			return d.rejected("样本没有实际测试 stdout 发射依据")
		}
		root, e := wireSchema(o, ref, pointer)
		if e != nil {
			return d.info(e.Error())
		}
		if e = wireProfileShape(profile, root, kind); e != nil {
			return d.rejected(e.Error())
		}
		raw, e := d.recordRef(text(row["sample_ref"]))
		if e != nil {
			return e
		}
		if "sha256:"+safefs.Digest(raw) != text(row["sample_digest"]) {
			return d.rejected("wire 样本原字节摘要陈旧")
		}
		sample, e := schema.Parse(raw)
		if e != nil {
			return d.info("wire 样本不可解析")
		}
		if e = wireValidate(root, sample); e != nil {
			return d.rejected(e.Error())
		}
		sampleObject, _ := object(sample)
		shape, _, _ := wireShape(root)
		for name, value := range sampleObject {
			field, _ := object(shape[name])
			if text(field["format"]) == "int32" || text(field["format"]) == "int64" {
				n, valid := integer(value)
				if !valid || text(field["format"]) == "int32" && (n < -2147483648 || n > 2147483647) {
					return d.rejected("wire 整数格式越界: " + name)
				}
			}
		}
		if kind == "PageResult" {
			p, _ := object(sample)
			shape, _, _ := wireShape(root)
			if shape["totalPages"] != nil && p["totalPages"] == nil {
				return d.rejected("totalPages 没有目标 mapper 样本")
			}
		}
		covered[ref+"#"+pointer] = true
		doc, _ := o.doc(ref)
		node, _ := dailyPointer(doc, pointer)
		closed, err := o.closure(ref, node)
		if err != nil {
			return d.info(err.Error())
		}
		for _, key := range wireRootRefs(closed) {
			covered[key] = true
		}
	}
	// Observe the complete root closure before scanning, including external files.
	doc, e := o.doc(ref)
	if e != nil {
		return e
	}
	if _, e = o.closure(ref, doc); e != nil {
		return d.info(e.Error())
	}
	var walk func(string, any, string) error
	walk = func(file string, v any, p string) error {
		switch x := v.(type) {
		case map[string]any:
			props, _ := object(x["properties"])
			isSchema := strings.HasSuffix(p, "/schema") || strings.HasPrefix(p, "/components/schemas/") && strings.Count(strings.TrimPrefix(p, "/components/schemas/"), "/") == 0
			needs := x["x-yss-response-wrapper"] != nil || x["x-yss-page-query"] != nil || props["code"] != nil && props["data"] != nil
			aliases := []string{}
			if isSchema {
				root, err := wireSchema(o, file, p)
				if err != nil {
					return d.info(err.Error())
				}
				kind, _, err := wireIdentity(root)
				shape, _, shapeErr := wireShape(root)
				needs = needs || kind != "" || shapeErr == nil && shape["data"] != nil && shape["code"] != nil
				if err != nil {
					return d.info(err.Error())
				}
				closed, err := o.closure(file, x)
				if err != nil {
					return d.info(err.Error())
				}
				aliases = wireRootRefs(closed)
			}
			if needs {
				found := covered[file+"#"+p]
				for _, alias := range aliases {
					found = found || covered[alias]
				}
				if !found {
					return d.rejected("未覆盖的目标 wire schema: " + file + "#" + p)
				}
			}
			for k, v := range x {
				if err := walk(file, v, p+"/"+strings.ReplaceAll(strings.ReplaceAll(k, "~", "~0"), "/", "~1")); err != nil {
					return err
				}
			}
		case []any:
			for i, v := range x {
				if err := walk(file, v, fmt.Sprintf("%s/%d", p, i)); err != nil {
					return err
				}
			}
		}
		return nil
	}
	files := make([]string, 0, len(o.docs))
	for file := range o.docs {
		files = append(files, file)
	}
	sort.Strings(files)
	for _, file := range files {
		if err := walk(file, o.docs[file], ""); err != nil {
			return err
		}
	}
	d.report.Checks = append(d.report.Checks, SemanticCheck{ID: "api.target-wire", SourceRef: d.taskRef, Status: "passed"})
	return nil
}

func dailyWireArray(v any) []any { rows, _ := v.([]any); return rows }

func wireIdentity(root map[string]any) (string, bool, error) {
	kind := ""
	objectOnly := false
	seen := map[string]bool{}
	var walk func(any) error
	walk = func(v any) error {
		m, ok := object(v)
		if !ok {
			return fmt.Errorf("invalid wrapper schema")
		}
		if text(m["type"]) == "object" {
			objectOnly = true
		}
		if m["x-yss-response-wrapper"] != nil {
			k := text(m["x-yss-response-wrapper"])
			if k == "" || kind != "" && kind != k {
				return fmt.Errorf("ambiguous wrapper annotation")
			}
			kind = k
		}
		if m["x-yss-page-query"] != nil {
			if m["x-yss-page-query"] != true || kind != "" && kind != "PageQuery" {
				return fmt.Errorf("invalid PageQuery annotation")
			}
			kind = "PageQuery"
		}
		if ref := text(m["$ref"]); ref != "" && !seen[ref] {
			seen[ref] = true
			v, e := dailyPointer(root, strings.TrimPrefix(ref, "#"))
			if e != nil {
				return e
			}
			if e = walk(v); e != nil {
				return e
			}
		}
		for _, v := range dailyWireArray(m["allOf"]) {
			if e := walk(v); e != nil {
				return e
			}
		}
		return nil
	}
	e := walk(root)
	return kind, objectOnly, e
}
func wireTestCommand(argv []string) bool {
	if len(argv) < 2 {
		return false
	}
	if argv[0] == "./mvnw" {
		for _, a := range argv[1:] {
			if a == "-DskipTests" || a == "-Dmaven.test.skip=true" || a == "-DskipTests=true" || a == "--version" || a == "-v" {
				return false
			}
		}
		return semHas(argv[1:], "test") || semHas(argv[1:], "verify") || semHas(argv[1:], "integration-test")
	}
	return argv[0] == "pnpm" && (argv[1] == "test" || argv[1] == "exec" && len(argv) > 3 && (argv[2] == "vitest" && argv[3] == "run" || argv[2] == "playwright" && argv[3] == "test"))
}
func wireEmitted(log map[string]any, mapper, kind, digest string) bool {
	for _, line := range strings.Split(text(log["stdout"]), "\n") {
		if !strings.HasPrefix(line, "YSS_WIRE_SAMPLE ") {
			continue
		}
		var row map[string]any
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "YSS_WIRE_SAMPLE ")), &row) == nil && text(row["mapper_id"]) == mapper && text(row["wrapper"]) == kind && text(row["sample_digest"]) == digest {
			return true
		}
	}
	return false
}

// Only references in the root composition alias a sample. A nullable nested
// payload does not prove serialization of another wrapper hidden in its refs.
func wireRootRefs(closed any) []string {
	c, _ := object(closed)
	refs, _ := object(c["reachable_refs"])
	seen := map[string]bool{}
	out := []string{}
	var walk func(any)
	walk = func(v any) {
		m, _ := object(v)
		// A common allOf fragment is not equivalent to the complete response.
		for k := range m {
			if k != "$ref" && k != "allOf" && k != "description" && k != "title" && k != "x-yss-response-wrapper" && k != "x-yss-page-query" {
				return
			}
		}
		if key := text(m["$ref"]); key != "" && !seen[key] && m["allOf"] == nil {
			seen[key] = true
			out = append(out, key)
			walk(refs[key])
			return
		}
		rows := dailyWireArray(m["allOf"])
		if len(rows) == 1 && m["$ref"] == nil {
			walk(rows[0])
		}
	}
	walk(c["value"])
	return out
}
