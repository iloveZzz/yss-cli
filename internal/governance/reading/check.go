package reading

import (
	"errors"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
)

const (
	readingPolicyRef       = ".template-spec/process/reading-policy.yaml"
	readingPolicySchemaRef = ".template-spec/process/schemas/reading-policy.schema.json"
	outputStalePrefix      = "阅读输出过期: "
)

// loadReadingPolicy 读取并校验阅读策略；文件不存在表示项目未启用（present 为 false）。
func loadReadingPolicy(s *kernel.SemanticSession) (map[string]any, bool, error) {
	exists, err := s.Exists(readingPolicyRef)
	if err != nil || !exists {
		return nil, false, err
	}
	policy, err := s.Doc(readingPolicyRef)
	if err != nil {
		return nil, false, err
	}
	if err = s.ValidateSchema(readingPolicySchemaRef, policy); err != nil {
		return nil, false, err
	}
	return policy, true, nil
}

func readingRecovery(ref string) string { return "yss reading render --checkpoint " + ref }

// CheckViews 只读判断 checkpoint 的阅读视图是否与当前来源一致。
// status 取值 manual（策略未启用且未要求）、current、stale；与 Node checkReadingViews 的键一致。
// stale 时第二个返回值为非空的 *ViewsStale；调用方据此以退出码 1 报告。
func CheckViews(s *kernel.SemanticSession, ref string, required bool) (map[string]any, error) {
	policy, present, err := loadReadingPolicy(s)
	if err != nil {
		return nil, err
	}
	enabled := present && policy["mode"] == "managed" && kernel.SemHas(policy["checkpoints"], ref)
	report := map[string]any{"read_only": true, "execution_allowed": false}
	if !required && !enabled {
		report["status"] = "manual"
		report["blockers"] = []string{}
		return report, nil
	}
	err = verifyReadingCurrent(s, ref)
	if err == nil {
		report["status"] = "current"
		report["approval_validity"] = "not-checked"
		report["blockers"] = []string{}
		report["diagnostics"] = []any{}
		report["recovery"] = readingRecovery(ref)
		return report, nil
	}
	var de *domain.Error
	if errors.As(err, &de) && de.Code == "CANCELLED" {
		return nil, err
	}
	report["status"] = "stale"
	report["recovery"] = readingRecovery(ref)
	blocker, compared := "", true
	switch {
	case !errors.As(err, &de):
		blocker, compared = err.Error(), false
	case de.Code == "READING_INCOMPLETE":
		blocker, compared = "reading-update-incomplete", false
	case de.Code == "READING_MANIFEST_STALE":
		blocker = "reading-manifest-stale"
	case de.Code == "READING_OUTPUT_STALE":
		blocker = "reading-output-stale: " + strings.TrimPrefix(de.Message, outputStalePrefix)
	case de.Code == "READING_NAVIGATION_STALE":
		blocker = "reading-navigation-stale"
	default:
		blocker, compared = de.Code+": "+de.Message, false
	}
	if compared {
		report["approval_validity"] = "not-checked"
		report["diagnostics"] = []any{}
	}
	report["blockers"] = []string{blocker}
	return report, &ViewsStale{Report: report}
}

// ViewsStale 让命令以退出码 1 报告 stale，同时把完整报告作为错误结果输出。
type ViewsStale struct{ Report map[string]any }

func (e *ViewsStale) Error() string {
	return "阅读视图已过期: " + strings.Join(e.Report["blockers"].([]string), "; ")
}
func (e *ViewsStale) ErrorResult() any { return e.Report }
func (e *ViewsStale) Unwrap() error {
	return &domain.Error{Code: "READING_STALE", Message: e.Error(), Exit: 1}
}

// AffectedCheckpoints 返回受某个资产变化影响的 managed checkpoint：
// 资产本身是该 checkpoint，或位于其功能目录之下。非阅读资产类型与未启用策略返回空集。
func AffectedCheckpoints(s *kernel.SemanticSession, assetRef, kind string) ([]string, error) {
	out := []string{}
	if !kernel.SemHas([]string{"domain-strategy", "stage-decision-package", "checkpoint", "tracking-migration"}, kind) {
		return out, nil
	}
	policy, present, err := loadReadingPolicy(s)
	if err != nil || !present || policy["mode"] != "managed" {
		return out, err
	}
	for _, item := range kernel.SemList(policy["checkpoints"]) {
		checkpoint := kernel.Text(item)
		doc, err := s.Doc(checkpoint)
		if err != nil {
			return nil, err
		}
		_, base, err := kernel.StageFeatureBinding(s, checkpoint, doc)
		if err != nil {
			return nil, err
		}
		if assetRef == checkpoint || strings.HasPrefix(assetRef, base+"/") {
			out = append(out, checkpoint)
		}
	}
	return out, nil
}

// VerifyCurrent 供写路径在提交前后复核：阅读材料必须与当前原生重算逐项一致。
func VerifyCurrent(s *kernel.SemanticSession, ref string) error { return verifyReadingCurrent(s, ref) }

// ManagedCheckpoints 返回阅读策略登记的 checkpoint 列表；策略未启用 managed 时返回空集。
func ManagedCheckpoints(s *kernel.SemanticSession) ([]string, error) {
	policy, present, err := loadReadingPolicy(s)
	if err != nil || !present || policy["mode"] != "managed" {
		return []string{}, err
	}
	out := []string{}
	for _, item := range kernel.SemList(policy["checkpoints"]) {
		out = append(out, kernel.Text(item))
	}
	return out, nil
}

// LoadPolicy 读取并校验阅读策略；present 为 false 表示策略文件不存在。
func LoadPolicy(s *kernel.SemanticSession) (map[string]any, bool, error) { return loadReadingPolicy(s) }
