package domain

import "fmt"

const Version = "2.7.4"
const ProtocolVersion = 1
const MetadataFile = ".yss.json"

type Error struct {
	Code    string
	Message string
	Exit    int
}

func (e *Error) Error() string        { return e.Message }
func Fail(code, message string) error { return &Error{code, message, 1} }
func Wrap(code string, err error) error {
	if err == nil {
		return nil
	}
	return &wrappedError{base: &Error{code, err.Error(), 1}, cause: err}
}

type Descriptor struct {
	Type   string `json:"type"`
	Digest string `json:"digest,omitempty"`
	Mode   uint32 `json:"mode,omitempty"`
}
type Profile struct {
	Name           string `json:"name"`
	ID             string `json:"profileId"`
	Metadata       string `json:"metadataFile"`
	LegacyCommand  string `json:"legacyCommand"`
	LegacyVersion  string `json:"legacyVersion"`
	TemplateName   string `json:"templateName"`
	TemplateSource string `json:"templateSource"`
}

var Profiles = map[string]Profile{
	"spec":    {"spec", "harness.spec-template", ".yss-template.json", "create-yss-spec", "3.5.10", "yss-spec-project-template", "github:iloveZzz/yss-spec-project-template"},
	"backend": {"backend", "harness.backend-delivery", ".yss-harness-backend.json", "create-yss-harness-backend", "0.4.21", "yss-harness-backend-agent", "github:iloveZzz/yss-harness-backend-agent"},
}

// 前端 Profile 已移除（AR-12c）：已有前端实例的文件原样保留，但不再被识别、升级或迁移。
func GetProfile(name string) (Profile, error) {
	p, ok := Profiles[name]
	if !ok && name == "design" {
		return p, Explain(Fail("IDENTITY", "Design Profile 已移除"), "PROFILE_REMOVED", "Design Profile 已移除；旧身份、迁移与历史证据不再受支持，原文件保留。", map[string]any{"profile": name})
	}
	if !ok && name == "frontend" {
		return p, Explain(Fail("IDENTITY", "前端 Profile 已移除"), "PROFILE_REMOVED", "前端 Profile 已从模板移除；已有前端实例不再识别、升级或迁移，文件保持原样、不会被删除。", map[string]any{"profile": name})
	}
	if !ok {
		return p, Fail("IDENTITY", fmt.Sprintf("未知 Profile: %s", name))
	}
	return p, nil
}

type Envelope struct {
	OutputVersion   int         `json:"outputVersion"`
	Version         string      `json:"version"`
	ProtocolVersion int         `json:"protocolVersion"`
	Command         string      `json:"command"`
	Profile         string      `json:"profile"`
	Status          string      `json:"status"`
	Code            string      `json:"code"`
	Result          any         `json:"result"`
	Diagnostic      *Diagnostic `json:"diagnostic,omitempty"`
}
