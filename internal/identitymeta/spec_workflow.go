package identitymeta

import "github.com/iloveZzz/yss-cli/internal/domain"

const MattSpecWorkflow = "matt-spec-v1"

// SpecWorkflow validates a declaration, not the provenance or installed resources.
func SpecWorkflow(meta *Metadata) (string, error) {
	if meta == nil {
		return "", nil
	}
	value, present := meta.Distribution["specWorkflow"]
	if !present {
		return "", nil
	}
	workflow, ok := value.(string)
	if !ok || workflow != MattSpecWorkflow || meta.Profile != "spec" || meta.SchemaVersion != 5 || meta.Distribution["installation"] != "governed" {
		return "", domain.Fail("SPEC_WORKFLOW_UNSUPPORTED", "Spec 工作流声明未知或与 Profile 不匹配")
	}
	return workflow, nil
}
