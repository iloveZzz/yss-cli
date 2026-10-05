package compat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/schema"
)

const APIVersion = 1

type errorRule struct {
	pattern *regexp.Regexp
	code    string
}

var errorRules = []errorRule{
	{regexp.MustCompile("模板路径越界|中间符号链接|目标父路径不是目录|符号链接或特殊文件"), "YSS_PATH_SAFETY"},
	{regexp.MustCompile("gitlink|git-submodule|detached HEAD|submodule"), "YSS_GIT_PROTECTED"},
	{regexp.MustCompile("模板家族|家族身份|多个模板家族|目标身份属于|profile 身份|身份不一致|身份文件格式非法|身份 metadata schema|未知或非法 profile|不支持跨家族迁移"), "YSS_FAMILY_IDENTITY_INVALID"},
	{regexp.MustCompile("ownership policy|user-owned|被 ownership policy 标记为 protected"), "YSS_OWNERSHIP_PROTECTED"},
	{regexp.MustCompile("模板快照|snapshotHash|templateCommit"), "YSS_SNAPSHOT_INVALID"},
	{regexp.MustCompile("模板元数据|metadataSchemaVersion|managedFilesManifestVersion"), "YSS_METADATA_INVALID"},
	{regexp.MustCompile("yss-project\\.yaml|repository_mode|schema_version"), "YSS_IDENTITY_INVALID"},
	{regexp.MustCompile("迁移冲突|迁移目标|旧路径迁移冲突"), "YSS_MIGRATION_CONFLICT"},
	{regexp.MustCompile("unsafe|受管路径阻断|人工整理 Ticket"), "YSS_UNSAFE_PATH"},
	{regexp.MustCompile("不支持的参数|需要 --|需要一个值|--dry-run 与 --apply|必须显式传入"), "YSS_ARGUMENT_INVALID"},
	{regexp.MustCompile("目标目录|目标路径"), "YSS_TARGET_INVALID"},
}

func mapSpecCode(code, message string) string {
	if strings.HasPrefix(code, "YSS_") {
		return code
	}
	if code == "UNPORTED" {
		return "YSS_UNPORTED"
	}
	for _, rule := range errorRules {
		if rule.pattern.MatchString(message) {
			return rule.code
		}
	}
	return "YSS_COMMAND_FAILED"
}

// ErrorEnvelope matches the Spec API's synchronous Error Envelope v1.
func ErrorEnvelope(message, code string) map[string]any {
	if message == "" {
		message = "未知错误"
	}
	return map[string]any{"schemaVersion": 1, "ok": false, "error": map[string]any{"code": mapSpecCode(code, message), "message": message}}
}

// RunAPI handles one bounded JSON request on stdin. Old methods fail explicitly
// until their complete success schemas have parity; native.run opts into v1.
func RunAPI(ctx context.Context, method string, stdin io.Reader, stdout, stderr io.Writer) int {
	raw, err := io.ReadAll(io.LimitReader(stdin, 4*1024*1024+1))
	if err != nil || len(raw) > 4*1024*1024 {
		return apiFailure(method, "ARGUMENT", "API 请求读取失败或超过 4 MiB", stdout)
	}
	var request map[string]json.RawMessage
	if !json.Valid(raw) {
		return apiFailure(method, "ARGUMENT", "API 请求必须是单个 JSON 文档", stdout)
	}
	if _, err = schema.Parse(raw); err != nil {
		return apiFailure(method, "ARGUMENT", err.Error(), stdout)
	}
	if err = json.Unmarshal(raw, &request); err != nil || request == nil {
		return apiFailure(method, "ARGUMENT", "API 请求必须是 JSON object", stdout)
	}
	if ctx.Err() != nil {
		return apiFailure(method, "CANCELLED", ctx.Err().Error(), stdout)
	}
	if method == "native.snapshot" {
		profile := "spec"
		if value, ok := request["profile"]; ok {
			if !jsonString(value) || json.Unmarshal(value, &profile) != nil {
				return apiFailure(method, "ARGUMENT", "profile 必须是字符串", stdout)
			}
		}
		for key := range request {
			if key != "profile" {
				return apiFailure(method, "ARGUMENT", "native.snapshot 不支持字段: "+key, stdout)
			}
		}
		b, err := bundle.Load(profile)
		if err != nil {
			return apiFailure(method, "BUNDLE", err.Error(), stdout)
		}
		result := map[string]any{"schemaVersion": 1, "profile": profile, "cliVersion": domain.Version, "protocolVersion": 1, "templateVersion": b.LegacyVersion, "cliCommit": b.CLICommit, "templateCommit": b.TemplateCommit, "sourceState": b.SourceState, "snapshotHash": b.SnapshotHash, "manifestHash": b.ManifestHash, "manifest": b.Manifest, "distribution": b.Distribution}
		_ = json.NewEncoder(stdout).Encode(domain.Envelope{OutputVersion: 1, Version: domain.Version, ProtocolVersion: 1, Command: "compat-api:" + method, Profile: profile, Status: "ok", Code: "OK", Result: result})
		return 0
	}
	if method == "native.run" {
		var args []string
		profile := "spec"
		if value, ok := request["args"]; !ok {
			return apiFailure(method, "ARGUMENT", "native.run 需要 args 数组", stdout)
		} else if err = json.Unmarshal(value, &args); err != nil || args == nil || len(args) == 0 {
			return apiFailure(method, "ARGUMENT", "args 必须是字符串数组", stdout)
		}
		var entries []json.RawMessage
		_ = json.Unmarshal(request["args"], &entries)
		for _, entry := range entries {
			if !jsonString(entry) {
				return apiFailure(method, "ARGUMENT", "args 每项必须是字符串，不能是 null", stdout)
			}
		}
		if value, ok := request["profile"]; ok {
			if !jsonString(value) {
				return apiFailure(method, "ARGUMENT", "profile 必须是字符串", stdout)
			}
			if err = json.Unmarshal(value, &profile); err != nil {
				return apiFailure(method, "ARGUMENT", "profile 必须是字符串", stdout)
			}
		}
		for key := range request {
			if key != "args" && key != "profile" {
				return apiFailure(method, "ARGUMENT", "native.run 不支持字段: "+key, stdout)
			}
		}
		p, err := domain.GetProfile(profile)
		if err != nil {
			return apiFailure(method, "IDENTITY", err.Error(), stdout)
		}
		var out, diagnostic bytes.Buffer
		code := runNative(ctx, p, append(append([]string{"--native"}, args...), "--json"), &out, &diagnostic)
		var envelope domain.Envelope
		decoder := json.NewDecoder(&out)
		if err = decoder.Decode(&envelope); err != nil || envelope.OutputVersion != 1 || envelope.ProtocolVersion != 1 || envelope.Status != "ok" && envelope.Status != "error" {
			return apiFailure(method, "PROTOCOL_MISMATCH", "原生执行未返回协议 1 JSON；该调用尚不支持 API 包装", stdout)
		}
		var tail any
		if err = decoder.Decode(&tail); err != io.EOF {
			return apiFailure(method, "PROTOCOL_MISMATCH", "原生执行返回多个 JSON 值", stdout)
		}
		_ = json.NewEncoder(stdout).Encode(envelope)
		if diagnostic.Len() > 0 {
			fmt.Fprint(stderr, diagnostic.String())
		}
		return code
	}
	if method == "toErrorEnvelope" {
		var message, code string
		if value, ok := request["message"]; ok {
			if json.Unmarshal(value, &message) != nil {
				return apiFailure(method, "ARGUMENT", "message 必须是字符串", stdout)
			}
		}
		if value, ok := request["code"]; ok {
			_ = json.Unmarshal(value, &code)
		}
		return apiSuccess(method, ErrorEnvelope(message, code), stdout)
	}
	switch method {
	case "projectDoctor", "projectDiff", "templatePlan", "templateApply":
		return apiFailure(method, "UNPORTED", legacyGap, stdout)
	default:
		return apiFailure(method, "ARGUMENT", "未知 API 方法: "+method, stdout)
	}
}
func jsonString(raw []byte) bool { raw = bytes.TrimSpace(raw); return len(raw) > 1 && raw[0] == '"' }
func apiSuccess(method string, result any, stdout io.Writer) int {
	_ = json.NewEncoder(stdout).Encode(domain.Envelope{OutputVersion: 1, Version: domain.Version, ProtocolVersion: 1, Command: "compat-api:" + method, Profile: "spec", Status: "ok", Code: "OK", Result: result})
	return 0
}
func apiFailure(method, code, message string, stdout io.Writer) int {
	_ = json.NewEncoder(stdout).Encode(domain.Envelope{OutputVersion: 1, Version: domain.Version, ProtocolVersion: 1, Command: "compat-api:" + method, Profile: "spec", Status: "error", Code: code, Result: map[string]any{"message": message, "legacyError": ErrorEnvelope(message, "YSS_"+code)}})
	return 1
}
