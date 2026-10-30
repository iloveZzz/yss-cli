package helpview

import (
	"encoding/json"
	"fmt"
	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func deriveSpecWorkflow(b *bundle.Bundle) (*View, bool, error) {
	if b.Profile != "spec" || b.Installations["governed"].Distribution["specWorkflow"] != "matt-spec-v1" {
		return nil, false, nil
	}
	f, ok := b.Files[".template-spec/process/spec-workflow.json"]
	if !ok {
		return nil, true, fmt.Errorf("new Spec source policy missing")
	}
	raw, err := f.Render(nil)
	if err != nil || safefs.Digest(raw) != f.Digest {
		return nil, true, fmt.Errorf("new Spec source policy digest mismatch")
	}
	var policy struct {
		SchemaVersion int    `json:"schemaVersion"`
		Profile       string `json:"profile"`
		Workflow      string `json:"specWorkflow"`
	}
	if err := json.Unmarshal(raw, &policy); err != nil || policy.SchemaVersion != 1 || policy.Profile != "spec" || policy.Workflow != "matt-spec-v1" {
		return nil, true, fmt.Errorf("unsupported Spec source policy")
	}
	return &View{SchemaVersion: 1, Profile: "spec", TemplateCommit: b.TemplateCommit, PolicySHA256: f.Digest, Workflow: policy.Workflow, Stages: []Stage{}, WorkflowSteps: []string{"初始化与按需技能就绪", "澄清业务、查证事实与统一词汇", "非技术规格及按风险选择产品设计", "按实际授权和审批发布到所选 Issue tracker，供下游引用"}}, true, nil
}
