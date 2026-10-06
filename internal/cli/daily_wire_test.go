package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/safefs"
)

const wireTestProfile = `schema_version: 1
kind: yss-dto-openapi-wire-profile
common_response:
  fields:
    success: {wire_types: [boolean], required: true, nullable: false}
    dataType: {wire_types: [string, "null"], required: true, nullable: true}
    code: {wire_types: [string, integer, "null"], required: true, nullable: true}
    message: {wire_types: [string, "null"], required: true, nullable: true}
    tips: {wire_types: [string, "null"], required: true, nullable: true}
wrappers:
  SingleResult:
    data: {required: true, nullable: true}
  MultiResult:
    data: {wire_types: [array], items: endpoint-schema, required: true, nullable: false}
  PageResult:
    fields:
      data: {wire_types: [array], items: endpoint-schema, required: true, nullable: false}
      totalCount: {wire_types: [integer], format: int64, minimum: 0, required: true, nullable: false}
      pageIndex: {wire_types: [integer], format: int32, minimum: 1, required: true, nullable: false}
      pageSize: {wire_types: [integer], format: int32, minimum: 1, required: true, nullable: false}
page_query:
  fields:
    pageIndex: {wire_types: [integer], format: int32, minimum: 1, default: 1}
    pageSize: {wire_types: [integer], format: int32, minimum: 1, default: 10}
    orderBy: {wire_types: [string, "null"], nullable: true}
    groupBy: {wire_types: [string, "null"], nullable: true}
    orderDirection: {wire_types: [string], enum: [ASC, DESC], default: DESC}
  forbidden_client_fields: [offset, needTotalCount, tempTotalCount]
`

func newDailyWireFixture(t *testing.T, kind string) *dailyFixture {
	f := newDailyAPIFixture(t)
	api := f.evidence["api"].(map[string]any)
	dailyWrite(t, f.root, ".agents/skills/yss-dto/references/openapi-wire-profile.yaml", []byte(wireTestProfile))
	api["rules"].([]any)[0].(map[string]any)["digest"] = "sha256:" + safefs.Digest([]byte(wireTestProfile))
	props := map[string]any{}
	required := []any{"success", "dataType", "code", "message", "tips", "data"}
	for _, name := range []string{"dataType", "code", "message", "tips"} {
		props[name] = map[string]any{"type": []any{"string", "null"}}
	}
	props["success"] = map[string]any{"type": "boolean"}
	props["data"] = map[string]any{"type": []any{"object", "null"}}
	sample := map[string]any{"success": true, "dataType": nil, "code": nil, "message": nil, "tips": nil, "data": nil}
	if kind == "MultiResult" || kind == "PageResult" {
		props["data"] = map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
		sample["data"] = []any{}
	}
	if kind == "PageResult" {
		for _, name := range []string{"totalCount", "pageSize", "pageIndex"} {
			format := "int32"
			minimum := 1
			if name == "totalCount" {
				format = "int64"
				minimum = 0
			}
			props[name] = map[string]any{"type": "integer", "format": format, "minimum": minimum}
			required = append(required, name)
			sample[name] = minimum
		}
	}
	node := map[string]any{"type": "object", "x-yss-response-wrapper": kind, "properties": props, "required": required}
	if kind == "PageQuery" {
		props = map[string]any{"pageIndex": map[string]any{"type": "integer", "format": "int32", "minimum": 1, "default": 1}, "pageSize": map[string]any{"type": "integer", "format": "int32", "minimum": 1, "default": 10}, "orderBy": map[string]any{"type": []any{"string", "null"}, "enum": []any{"id", nil}}, "groupBy": map[string]any{"type": []any{"string", "null"}, "enum": []any{"id", nil}}, "orderDirection": map[string]any{"type": "string", "enum": []any{"ASC", "DESC"}, "default": "DESC"}}
		node = map[string]any{"type": "object", "x-yss-page-query": true, "additionalProperties": false, "properties": props}
		sample = map[string]any{"pageIndex": 1, "pageSize": 10, "orderDirection": "DESC"}
	}
	encoded, _ := json.Marshal(node)
	raw, _ := os.ReadFile(filepath.Join(f.repo, "api.yaml"))
	raw = bytes.Replace(raw, []byte("schema: {type: string}"), append([]byte("schema: "), encoded...), 1)
	dailyWrite(t, f.repo, "api.yaml", raw)
	api["candidate"].(map[string]any)["digest"] = "sha256:" + safefs.Digest(raw)
	dailyWrite(t, f.repo, "mapper.json", []byte(`{"mapper":"target-http"}`))
	dailyGit(t, f.repo, "add", "mapper.json")
	dailyGit(t, f.repo, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "mapper basis")
	f.base = dailyGit(t, f.repo, "rev-parse", "HEAD")
	f.evidence["repository"].(map[string]any)["baseline_sha"] = f.base
	f.evidence["commands"] = append(f.evidence["commands"].([]any), map[string]any{"argv": []any{"./mvnw", "test"}, "cwd": "."})
	sampleRaw, _ := json.Marshal(sample)
	dailyWrite(t, f.root, "sample.json", sampleRaw)
	api["wire"] = map[string]any{"applicability": "applicable", "mapper": map[string]any{"id": "target-http", "inputs": []any{map[string]any{"ref": "mapper.json", "digest": "sha256:" + safefs.Digest([]byte(`{"mapper":"target-http"}`))}}}, "samples": []any{map[string]any{"oas_pointer": "/paths/~1new/get/responses/200/content/application~1json/schema", "wrapper": kind, "sample_ref": "sample.json", "sample_digest": "sha256:" + safefs.Digest(sampleRaw)}}}
	runtime := map[string]any{"id": "target-http", "registered_modules": []any{}, "serialization_features": 0, "artifact_digests": map[string]any{"dto": "sha256:" + strings.Repeat("a", 64)}}
	runtimeRaw, _ := json.Marshal(runtime)
	dailyWrite(t, f.root, "mapper-runtime.json", runtimeRaw)
	api["wire"].(map[string]any)["mapper"].(map[string]any)["runtime"] = map[string]any{"ref": "mapper-runtime.json", "digest": "sha256:" + safefs.Digest(runtimeRaw)}
	f.save(t)
	return f
}
func (f *dailyFixture) completeWire(t *testing.T) map[string]any {
	api := f.evidence["api"].(map[string]any)
	w := api["wire"].(map[string]any)
	r := f.completeAPI(t)
	api["wire"] = w
	raw := dailyExecutionJSON(f.repo, []string{"./mvnw", "test"}, 0, "api_digest", r["api_digest"].(string))
	var log map[string]any
	if e := json.Unmarshal([]byte(raw), &log); e != nil {
		t.Fatal(e)
	}
	log["mapper_id"] = "target-http"
	log["samples"] = w["samples"]
	log["candidate_digest"] = r["candidate_digest"]
	runtimeRaw, _ := os.ReadFile(filepath.Join(f.root, "mapper-runtime.json"))
	emissions := "YSS_WIRE_MAPPER " + string(runtimeRaw) + "\n"
	for _, v := range w["samples"].([]any) {
		row := v.(map[string]any)
		raw, _ := json.Marshal(map[string]any{"mapper_id": "target-http", "wrapper": row["wrapper"], "sample_digest": row["sample_digest"]})
		emissions += "YSS_WIRE_SAMPLE " + string(raw) + "\n"
	}
	log["stdout"] = emissions
	b, _ := json.Marshal(log)
	section := string(b) + "\n"
	f.sections += "## wire校验\n" + section
	w["argv"] = []any{"./mvnw", "test"}
	w["exit_code"] = 0
	w["api_digest"] = r["api_digest"]
	w["log_ref"] = "#wire校验"
	w["log_digest"] = "sha256:" + safefs.Digest([]byte(section))
	f.evidence["tests"] = append(f.evidence["tests"].([]any), map[string]any{"command_index": 1, "exit_code": 0, "log_ref": "#wire校验", "log_digest": w["log_digest"], "candidate_digest": r["candidate_digest"]})
	api["contract_tests"] = f.evidence["tests"]
	f.save(t)
	return r
}
func TestDailyNativeWireAcceptsActualSamplesAndBlocksDrift(t *testing.T) {
	for _, kind := range []string{"SingleResult", "MultiResult", "PageResult", "PageQuery"} {
		t.Run(kind, func(t *testing.T) {
			f := newDailyWireFixture(t, kind)
			f.completeWire(t)
			if code, e := f.run(t, "verify-daily"); code != 0 {
				t.Fatalf("wire positive %d %#v", code, e)
			}
			dailyWrite(t, f.root, "sample.json", []byte(`{"success":"wrong"}`))
			if code, e := f.run(t, "verify-daily"); code != 1 {
				t.Fatalf("sample drift allowed %d %#v", code, e)
			}
		})
	}
}
func TestDailyNativeWireRejectsInvalidShapeAndSample(t *testing.T) {
	for _, kind := range []string{"bad-type", "missing-required", "paging-zero", "paging-overflow", "fake-log", "version-command", "mapper-drift"} {
		t.Run(kind, func(t *testing.T) {
			f := newDailyWireFixture(t, "PageResult")
			f.completeWire(t)
			api := f.evidence["api"].(map[string]any)
			w := api["wire"].(map[string]any)
			switch kind {
			case "bad-type", "missing-required", "paging-zero", "paging-overflow":
				raw, _ := os.ReadFile(filepath.Join(f.root, "sample.json"))
				var sample map[string]any
				json.Unmarshal(raw, &sample)
				if kind == "bad-type" {
					sample["success"] = "true"
				}
				if kind == "missing-required" {
					delete(sample, "data")
				}
				if kind == "paging-overflow" {
					sample["pageSize"] = int64(2147483648)
				}
				if kind == "paging-zero" {
					sample["pageIndex"] = 0
				}
				raw, _ = json.Marshal(sample)
				dailyWrite(t, f.root, "sample.json", raw)
				rows := w["samples"].([]any)
				old := rows[0].(map[string]any)["sample_digest"].(string)
				next := "sha256:" + safefs.Digest(raw)
				rows[0].(map[string]any)["sample_digest"] = next
				f.sections = strings.ReplaceAll(f.sections, old, next)
				p := strings.Split(f.sections, "## wire校验\n")[1]
				w["log_digest"] = "sha256:" + safefs.Digest([]byte(p))
				f.evidence["tests"].([]any)[1].(map[string]any)["log_digest"] = w["log_digest"]
			case "version-command":
				w["argv"] = []any{"./mvnw", "--version"}
			case "fake-log":
				w["argv"] = []any{"echo", "target-wire"}
			case "mapper-drift":
				dailyWrite(t, f.repo, "mapper.json", []byte(`{"mapper":"changed"}`))
				f.evidence["scope"].(map[string]any)["paths"] = append(f.evidence["scope"].(map[string]any)["paths"].([]any), "mapper.json")
			}
			f.save(t)
			if code, e := f.run(t, "verify-daily"); code == 0 {
				t.Fatalf("%s accepted %#v", kind, e)
			}
		})
	}
}

func TestDailyNativeWireCannotRelabelWrapperOrAcceptPrimitive(t *testing.T) {
	for _, kind := range []string{"relabel", "primitive", "missing-object"} {
		t.Run(kind, func(t *testing.T) {
			f := newDailyWireFixture(t, "MultiResult")
			p := filepath.Join(f.repo, "api.yaml")
			raw, _ := os.ReadFile(p)
			if kind == "relabel" {
				raw = bytes.Replace(raw, []byte(`"x-yss-response-wrapper":"MultiResult"`), []byte(`"x-yss-response-wrapper":"SingleResult"`), 1)
			} else {
				raw = bytes.Replace(raw, []byte(`"type":"object",`), []byte(``), 1)
			}
			dailyWrite(t, f.repo, "api.yaml", raw)
			f.evidence["api"].(map[string]any)["candidate"].(map[string]any)["digest"] = "sha256:" + safefs.Digest(raw)
			if kind == "primitive" {
				sample := []byte(`"primitive"`)
				dailyWrite(t, f.root, "sample.json", sample)
				f.evidence["api"].(map[string]any)["wire"].(map[string]any)["samples"].([]any)[0].(map[string]any)["sample_digest"] = "sha256:" + safefs.Digest(sample)
			}
			f.save(t)
			f.completeWire(t)
			if code, e := f.run(t, "verify-daily"); code == 0 {
				t.Fatalf("%s passed %#v", kind, e)
			}
		})
	}
}

func TestDailyNativeWireRequiresReachableExternalAndQuerySamples(t *testing.T) {
	for _, kind := range []string{"external-wrapper", "external-query", "nested-wrapper"} {
		t.Run(kind, func(t *testing.T) {
			f := newDailyWireFixture(t, "MultiResult")
			api := f.evidence["api"].(map[string]any)
			p := filepath.Join(f.repo, "api.yaml")
			raw, _ := os.ReadFile(p)
			extra := `{"type":"object","x-yss-page-query":true,"properties":{"pageIndex":{"type":"integer"}}}`
			if kind != "external-query" {
				extra = `{"type":"object","x-yss-response-wrapper":"SingleResult","properties":{"code":{"type":"string"},"data":{"type":["string","null"]}}}`
			}
			dailyWrite(t, f.repo, "external.yaml", []byte("Other: "+extra+"\n"))
			scope := f.evidence["scope"].(map[string]any)
			scope["paths"] = append(scope["paths"].([]any), "external.yaml")
			if kind == "nested-wrapper" {
				raw = bytes.Replace(raw, []byte(`"items":{"type":"string"}`), []byte(`"items":{"$ref":"external.yaml#/Other"}`), 1)
			} else {
				raw = append(raw, []byte("      parameters:\n        - name: payload\n          in: query\n          content:\n            application/json:\n              schema: {$ref: 'external.yaml#/Other'}\n")...)
			}
			dailyWrite(t, f.repo, "api.yaml", raw)
			api["candidate"].(map[string]any)["digest"] = "sha256:" + safefs.Digest(raw)
			f.save(t)
			f.completeWire(t)
			if code, e := f.run(t, "verify-daily"); code == 0 {
				t.Fatalf("missing reachable %s accepted %#v", kind, e)
			}
		})
	}
}

func TestDailyNativeWireCommonMetaCannotCoverDifferentResponse(t *testing.T) {
	f := newDailyWireFixture(t, "MultiResult")
	api := f.evidence["api"].(map[string]any)
	raw, _ := os.ReadFile(filepath.Join(f.repo, "api.yaml"))
	raw = bytes.Replace(raw, []byte(`"properties":{`), []byte(`"allOf":[{"$ref":"#/components/schemas/Meta"}],"properties":{`), 1)
	raw = append(raw, []byte("  /unobserved:\n    get:\n      operationId: getUnobserved\n      responses:\n        '200':\n          description: ok\n          content:\n            application/json:\n              schema:\n                x-yss-response-wrapper: SingleResult\n                allOf:\n                  - {$ref: '#/components/schemas/Meta'}\n                  - {type: object, required: [data], properties: {data: {type: [string, 'null']}}}\ncomponents:\n  schemas:\n    Meta: {type: object}\n")...)
	dailyWrite(t, f.repo, "api.yaml", raw)
	api["candidate"].(map[string]any)["digest"] = "sha256:" + safefs.Digest(raw)
	api["new_operations"] = append(api["new_operations"].([]any), map[string]any{"path": "/unobserved", "method": "get", "operation_id": "getUnobserved"})
	f.save(t)
	f.completeWire(t)
	if code, e := f.run(t, "verify-daily"); code == 0 {
		t.Fatalf("shared metadata covered unobserved response %#v", e)
	}
}
