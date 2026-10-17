package kernel

import (
	"path/filepath"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
)

// Only bound inputs of already validated implementation/review consumers supply requirements.
func (s *SemanticSession) RequiredReviewMode(candidate, implementation map[string]any, scope []string, bindings ...any) (string, error) {
	return s.requiredReviewMode(candidate, implementation, scope, scope, "", bindings...)
}

// The validated task maps approval scope (for example feature.orders) to its filesystem write scope.
func (s *SemanticSession) RequiredReviewModeForTask(candidate, implementation map[string]any, scope, paths []string, bindings ...any) (string, error) {
	return s.requiredReviewMode(candidate, implementation, scope, paths, "", bindings...)
}

// Daily consumers have already verified their Markdown Ticket and registered implementation root.
func (s *SemanticSession) RequiredReviewModeAtRoot(candidate, implementation map[string]any, scope []string, root string, bindings ...any) (string, error) {
	if Text(implementation["task_ref"]) == "" || root == "" {
		return "", s.Reject("EVIDENCE_REVIEW_REQUIREMENT", "缺少真实任务和已验证实施根")
	}
	if _, err := s.Bytes(Text(implementation["task_ref"])); err != nil {
		return "", err
	}
	if err := s.reviewRoot(root); err != nil {
		return "", err
	}
	return s.requiredReviewMode(candidate, implementation, scope, scope, root, bindings...)
}

func (s *SemanticSession) requiredReviewMode(candidate, implementation map[string]any, scope, paths []string, root string, bindings ...any) (string, error) {
	mode := "self-check"
	seen := map[string]string{}
	for _, basis := range bindings {
		for _, asset := range ApRows(basis) {
			ref, digest := Text(asset["ref"]), Text(asset["digest"])
			if old, ok := seen[ref]; ok {
				if old != digest {
					return "", s.Reject("EVIDENCE_BINDING", "复核要求输入摘要冲突")
				}
				continue
			}
			seen[ref] = digest
			raw, err := s.EvidenceAsset(asset)
			if err != nil {
				return "", err
			}
			if strings.EqualFold(filepath.Ext(ref), ".md") {
				raw, _, err = Frontmatter(raw)
				if err != nil {
					continue
				}
			}
			text := strings.TrimSpace(string(raw))
			if strings.HasPrefix(ref, "#") && strings.HasPrefix(text, "```json\n") && strings.HasSuffix(text, "\n```") {
				raw = []byte(strings.TrimSuffix(strings.TrimPrefix(text, "```json\n"), "\n```"))
			}
			value, err := schema.Parse(raw)
			if err != nil {
				continue
			}
			record, ok := Object(value)
			if !ok {
				continue
			}
			if rules, exists := Object(record["external_rules"]); exists {
				if err = s.ValidateExternalRulesDigest(rules, candidate, scope, implementation); err != nil {
					return "", err
				}
			}
			if record["kind"] != "review-requirement" {
				continue
			}
			if !ApUnique(record["scope"]) {
				return "", s.Reject("EVIDENCE_REVIEW_REQUIREMENT", "已知复核范围不可判定")
			}
			if err = s.Verify("review-requirement", ref, nil); err != nil {
				return "", err
			}
			applies, err := s.reviewScopesIntersectAtRoot(implementation, paths, ApStrings(record["scope"]), root)
			if err != nil {
				return "", err
			}
			if !applies {
				continue
			}
			mode = "independent-review"
		}
	}
	return mode, nil
}

// Scope paths are relative to the verified implementation repository. The
// caller supplied provenance comes from completed task revalidation, not review
// declarations; unregistered roots or ambiguous paths are input gaps.
func (s *SemanticSession) reviewScopesIntersect(implementation map[string]any, actual, requested []string) (bool, error) {
	return s.reviewScopesIntersectAtRoot(implementation, actual, requested, "")
}

func (s *SemanticSession) reviewRoot(root string) error {
	if s.IsTaskSnapshotRoot(root) {
		return nil
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || (root != s.Root && s.ExternalViews[root] == nil) {
		return s.Reject("EVIDENCE_REVIEW_REQUIREMENT", "无法确认独立复核要求的实际实施仓库")
	}
	return nil
}

func (s *SemanticSession) reviewScopesIntersectAtRoot(implementation map[string]any, actual, requested []string, root string) (bool, error) {
	if root == "" {
		root = s.Root
		if taskRef := Text(implementation["task_ref"]); taskRef != "" {
			task, err := s.Doc(taskRef)
			if err != nil {
				return false, err
			}
			root = Text(ApMap(task["baseline"])["repository_root"])
		}
	}
	if err := s.reviewRoot(root); err != nil {
		return false, err
	}
	if !ApUnique(actual) || !ApUnique(requested) {
		return false, s.Reject("EVIDENCE_REVIEW_REQUIREMENT", "无法确认实际复核路径范围")
	}
	var spellings safefs.PathSet
	paths := func(refs []string) ([]string, error) {
		out := []string{}
		for _, ref := range refs {
			ref = strings.TrimSuffix(ref, "/")
			if err := spellings.Add(ref); err != nil {
				return nil, s.Reject("EVIDENCE_REVIEW_REQUIREMENT", "复核路径无法安全确定: "+err.Error())
			}
			path, err := s.TaskPath(root, ref)
			if err != nil {
				return nil, s.Reject("EVIDENCE_REVIEW_REQUIREMENT", "复核路径无法安全确定: "+err.Error())
			}
			if s.IsTaskSnapshotRoot(root) {
				// Manifest storage aliases need not preserve the source directory hierarchy.
				path = filepath.FromSlash(ref)
			}
			out = append(out, path)
		}
		return out, nil
	}
	a, err := paths(actual)
	if err != nil {
		return false, err
	}
	b, err := paths(requested)
	if err != nil {
		return false, err
	}
	for _, left := range a {
		for _, right := range b {
			if left == right || strings.HasPrefix(left, right+string(filepath.Separator)) || strings.HasPrefix(right, left+string(filepath.Separator)) {
				return true, nil
			}
		}
	}
	return false, nil
}
