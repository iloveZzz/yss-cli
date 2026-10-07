package project

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"os"
	"path/filepath"
)

// Binding is an explicit opaque plugin record. Only the two public plugin
// locations are writable. The bytes travel in the digest-bound saved plan.
type Binding struct {
	SchemaVersion        int               `json:"schemaVersion"`
	Path                 string            `json:"path"`
	Data                 string            `json:"data"`
	PreviousDigest       string            `json:"previousDigest,omitempty"`
	Guards               map[string]string `json:"guards,omitempty"`
	LegacyBaselinePolicy string            `json:"legacyBaselinePolicy,omitempty"`
}

func ReadBinding(file string) (*Binding, error) {
	info, e := os.Lstat(file)
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() || info.Size() > 4*1024*1024 {
		return nil, domain.Fail("BINDING", "binding input must be a regular file at most 4 MiB")
	}
	raw, e := os.ReadFile(file)
	if e != nil {
		return nil, e
	}
	if !json.Valid(raw) {
		return nil, domain.Fail("BINDING", "binding request must be JSON")
	}
	if _, e = schema.Parse(raw); e != nil {
		return nil, e
	}
	var b Binding
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e = d.Decode(&b); e != nil {
		return nil, domain.Wrap("BINDING", e)
	}
	return &b, nil
}
func (b *Binding) validate(profile, command string) ([]byte, error) {
	if b.SchemaVersion != 1 {
		return nil, domain.Fail("BINDING", "unknown binding request schema")
	}
	if command != "init" && command != "attach" && command != "migrate" && command != "sync" {
		return nil, domain.Fail("BINDING", "binding supports init, attach, migrate or sync")
	}
	if b.LegacyBaselinePolicy != "" && (command != "migrate" || (profile != "spec" && profile != "design")) {
		return nil, domain.Fail("LEGACY_POLICY", "historical baseline policies only support explicit legacy plugin migration")
	}
	if !(b.Path == ".yss-backend-plugin.json" && profile == "spec") && !(b.Path == ".yss-product-design-plugin.json" && profile == "design") {
		return nil, domain.Fail("BINDING", "binding path does not match plugin profile")
	}
	data, e := base64.StdEncoding.DecodeString(b.Data)
	if e != nil || len(data) > 2*1024*1024 || !json.Valid(data) {
		return nil, domain.Fail("BINDING", "binding data must be a JSON object of at most 2 MiB")
	}
	value, e := schema.Parse(data)
	if e != nil {
		return nil, e
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, domain.Fail("BINDING", "binding data must be a JSON object")
	}
	if b.PreviousDigest != "" && !digestPattern.MatchString(b.PreviousDigest) {
		return nil, domain.Fail("BINDING", "previousDigest must be SHA-256")
	}
	return data, nil
}
func addBinding(p *Plan, b *Binding) error {
	if b == nil {
		return nil
	}
	data, e := b.validate(p.Profile, p.Command)
	if e != nil {
		return e
	}
	if b.Path == ".yss-product-design-plugin.json" {
		var payload map[string]any
		_ = json.Unmarshal(data, &payload)
		if number(payload["schema_version"]) != 2 || payload["plugin"] != "yss-product-design" || payload["profile"] != "design" {
			return domain.Fail("BINDING", "design plugin binding must match the native design profile")
		}
	}
	var payload map[string]any
	_ = json.Unmarshal(data, &payload)
	current, e := bundle.Load(p.Profile)
	if e != nil {
		return e
	}
	binaryDigest, e := domain.ExecutableDigest()
	if e != nil {
		return e
	}
	if payload["profile"] != p.Profile || payload["template_commit"] != p.TemplateCommit || payload["bundle_hash"] != current.BundleHash || payload["binary_sha256"] != binaryDigest {
		return domain.Fail("BINDING", "new plugin binding must match the current profile, template, Bundle and executable")
	}
	before, e := safefs.Describe(p.Root, b.Path)
	if e != nil {
		return e
	}
	if before.Type == "missing" {
		if b.PreviousDigest != "" {
			return domain.Fail("BINDING_CONFLICT", "previous binding is missing")
		}
	} else if before.Type != "file" || b.PreviousDigest == "" || before.Digest != b.PreviousDigest {
		return domain.Fail("BINDING_CONFLICT", "existing binding requires its explicit matching previousDigest")
	}
	for ref, digest := range b.Guards {
		if ref != ".yss-plugin.json" || !digestPattern.MatchString(digest) {
			return domain.Fail("BINDING", "unsupported legacy binding guard")
		}
		guard, e := safefs.Describe(p.Root, ref)
		if e != nil {
			return e
		}
		if guard.Type != "file" || guard.Digest != digest {
			return domain.Fail("BINDING_CONFLICT", "legacy binding guard no longer matches")
		}
		p.Inputs[ref] = guard
	}
	if b.Path == ".yss-backend-plugin.json" {
		var payload map[string]any
		_ = json.Unmarshal(data, &payload)
		if payload["execution_scope"] != "plan-to-backend" || payload["plugin"] != "yss-backend-delivery" || number(payload["schema_version"]) != 2 {
			return domain.Fail("BINDING", "backend plugin binding must retain plan-to-backend scope")
		}
		scopeRef := ".yss-execution-scope.yaml"
		scopeBytes := []byte("schema_version: 1\nscope_id: plan-to-backend\n")
		scopeBefore, e := safefs.Describe(p.Root, scopeRef)
		if e != nil {
			return e
		}
		p.Inputs[scopeRef] = scopeBefore
		if scopeBefore.Type != "missing" {
			existing, e := os.ReadFile(filepath.Join(p.Root, scopeRef))
			if e != nil {
				return e
			}
			v, e := schema.Parse(existing)
			if e != nil {
				return e
			}
			m, ok := v.(map[string]any)
			if !ok || len(m) != 2 || number(m["schema_version"]) != 1 || m["scope_id"] != "plan-to-backend" {
				return domain.Fail("BINDING_CONFLICT", "existing plugin scope must not be overwritten")
			}
		}
		if scopeBefore.Type == "missing" {
			scopeAfter := domain.Descriptor{Type: "file", Digest: safefs.Digest(scopeBytes), Mode: 0644}
			p.Changes = append(p.Changes, Change{scopeRef, scopeBefore, scopeAfter, base64.StdEncoding.EncodeToString(scopeBytes), "plugin-scope"})
			addSystemAsset(p, scopeRef, scopeBefore, scopeAfter, "plugin-scope")
		} else {
			addSystemAsset(p, scopeRef, scopeBefore, scopeBefore, "plugin-scope")
		}
	}
	copy := *b
	p.Binding = &copy
	p.Inputs[b.Path] = before
	mode := uint32(0644)
	if before.Type == "file" {
		mode = before.Mode
	}
	after := domain.Descriptor{Type: "file", Digest: safefs.Digest(data), Mode: mode}
	if before != after {
		p.Changes = append(p.Changes, Change{b.Path, before, after, b.Data, "plugin-binding"})
	}
	addSystemAsset(p, b.Path, before, after, "plugin-binding")
	finalizePlan(p)
	return nil
}
func BuildWithBinding(root, profile, command string, vars map[string]string, selection []string, b *Binding) (*Plan, error) {
	return BuildWithOptions(root, profile, command, vars, selection, b, PlanningOptions{})
}

// Existing receipts are inputs even when this operation does not change them.
// A source upgrade of a bound project needs its public plugin's new receipt.
func guardPluginBindings(id *Identity, source *bundle.Bundle, p *Plan, next *Binding) error {
	for _, ref := range []string{".yss-backend-plugin.json", ".yss-product-design-plugin.json", ".yss-plugin.json"} {
		observed, err := safefs.Describe(id.Root, ref)
		if err != nil {
			return err
		}
		p.Inputs[ref] = observed
		if observed.Type == "missing" {
			continue
		}
		if observed.Type != "file" {
			return domain.Fail("BINDING_CONFLICT", "plugin receipt must be a regular file: "+ref)
		}
		if next != nil && (ref == next.Path || ref == ".yss-plugin.json") {
			continue
		}
		if ref == ".yss-plugin.json" {
			if id.Native == nil && p.Command != "diff" && p.Command != "doctor" {
				return domain.Fail("BINDING_REQUIRED", "historical plugin project requires its public plugin migration plan")
			}
			continue
		}
		if id.Native == nil {
			return domain.Fail("BINDING_CONFLICT", "native plugin receipt has no matching native identity")
		}
		data, err := load(id.Root, ref)
		if err != nil {
			return err
		}
		receipt, ok := object(data)
		expected := ".yss-backend-plugin.json"
		plugin := "yss-backend-delivery"
		if id.Profile.Name == "design" {
			expected = ".yss-product-design-plugin.json"
			plugin = "yss-product-design"
		}
		if !ok || ref != expected || number(receipt["schema_version"]) != 2 || receipt["plugin"] != plugin || receipt["profile"] != id.Profile.Name || receipt["template_commit"] != id.Native.TemplateCommit || receipt["bundle_hash"] != id.Native.BundleHash || !digestPattern.MatchString(text(receipt["binary_sha256"])) {
			return domain.Explain(domain.Fail("BINDING_CONFLICT", "plugin receipt and native source identity disagree; use the public plugin upgrade plan"), "PLUGIN_BINDING_SOURCE_MISMATCH", "插件收据与原生项目的 Profile、模板或 Bundle 来源不一致。", map[string]any{"root": id.Root, "path": ref, "plugin": plugin, "templateCommit": id.Native.TemplateCommit, "bindingTemplateCommit": receipt["template_commit"]})
		}
		if p.Command == "diff" || p.Command == "doctor" {
			continue
		}
		binaryDigest, err := domain.ExecutableDigest()
		if err != nil {
			return err
		}
		provenance := domain.BuildProvenance()
		if id.Native.TemplateCommit != source.TemplateCommit || id.Native.BundleHash != source.BundleHash || id.Native.CLIVersion != domain.Version || id.Native.CLICommit != provenance.Commit || id.Native.CLISourceState != provenance.SourceState || receipt["binary_sha256"] != binaryDigest {
			return domain.Explain(domain.Fail("BINDING_REQUIRED", "bound project source changes require its public plugin upgrade plan and new binding"), "PLUGIN_BINDING_UPGRADE_REQUIRED", "程序或固定模板来源已变化，绑定项目必须由对应插件生成升级计划与新 binding。", map[string]any{"root": id.Root, "path": ref, "plugin": plugin, "templateCommit": id.Native.TemplateCommit, "instanceCLI": id.Native.CLIVersion})
		}
	}
	return nil
}
