package project

import (
	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/identitymeta"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

const SpecWorkflowRef = ".template-spec/process/spec-workflow.json"

// ValidateSpecWorkflow binds the declaration to a managed, unmodified policy
// from a supporting bundle. Native metadata validity is checked by Detect.
func ValidateSpecWorkflow(id *Identity, b *bundle.Bundle) error {
	if id == nil || id.Profile.Name != "spec" {
		return nil
	}
	workflow, err := identitymeta.SpecWorkflow(id.Native)
	if err != nil {
		return err
	}
	if workflow == "" {
		return domain.Fail("SPEC_MANUAL_MIGRATION_REQUIRED", "旧 Spec 实例须人工迁移；不会自动转换工作流")
	}
	if err := validateInstalledSpecWorkflow(id); err != nil {
		return err
	}
	if b == nil || b.Profile != "spec" || text(b.Distribution["specWorkflow"]) != workflow {
		return domain.Fail("SPEC_WORKFLOW_UNSUPPORTED", "当前 Bundle 不支持实例声明的 Spec 工作流")
	}
	expected, ok := b.Files[SpecWorkflowRef]
	managed, present := id.Native.Managed[SpecWorkflowRef]
	if !ok || !present || expected.Digest == "" || managed.Baseline.Digest != expected.Digest || managed.Applied.Digest != expected.Digest {
		return domain.Fail("SPEC_WORKFLOW_SOURCE", "Spec 工作流缺少受管来源政策")
	}
	actual, err := safefs.Describe(id.Root, SpecWorkflowRef)
	if err != nil {
		return err
	}
	if actual.Digest != expected.Digest || actual.Type != "file" {
		return domain.Fail("SPEC_WORKFLOW_SOURCE", "Spec 工作流政策已变化或不可用")
	}
	return nil
}

// IsMattSpec is a routing predicate for an identity already returned by Detect.
func IsMattSpec(id *Identity) bool {
	if id == nil || id.Native == nil || id.Profile.Name != "spec" {
		return false
	}
	workflow, err := identitymeta.SpecWorkflow(id.Native)
	return err == nil && workflow == identitymeta.MattSpecWorkflow
}

func validateInstalledSpecWorkflow(id *Identity) error {
	managed, ok := id.Native.Managed[SpecWorkflowRef]
	if !ok || managed.Baseline.Digest == "" || managed.Baseline.Digest != managed.Applied.Digest || managed.Source == nil || managed.Source.Kind != "bundle" || managed.Source.Path != SpecWorkflowRef || managed.Source.TemplateDigest != managed.Baseline.Digest {
		return domain.Fail("SPEC_WORKFLOW_SOURCE", "缺少受管 Spec 工作流政策")
	}
	actual, err := safefs.Describe(id.Root, SpecWorkflowRef)
	if err != nil {
		return err
	}
	if actual.Type != "file" || actual.Digest != managed.Applied.Digest || actual.Mode != managed.Applied.Mode {
		return domain.Fail("SPEC_WORKFLOW_SOURCE", "Spec 工作流政策与安装来源不一致")
	}
	return nil
}
