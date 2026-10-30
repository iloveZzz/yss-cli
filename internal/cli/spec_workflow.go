package cli

import (
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/project"
	"os"
	"path/filepath"
)

// These commands belong to the product lifecycle, not the Spec task workflow.
func rejectLegacySpecCommand(root, command string) error {
	switch command {
	case "lifecycle", "stage", "handoff", "profile", "reading", "contract", "evidence", "project-ci":
	default:
		return nil
	}
	if _, err := os.Lstat(filepath.Join(root, domain.MetadataFile)); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	id, err := project.Detect(root, "", false)
	if err != nil {
		return err
	}
	if id.Profile.Name != "spec" {
		return nil
	}
	if project.IsMattSpec(id) {
		return domain.Fail("SPEC_COMMAND_NOT_APPLICABLE", "新 Spec 使用技能与 Issue tracker；旧阶段、交接及实施命令不适用")
	}
	if id.Native != nil && id.Native.Distribution["installation"] == "governed" {
		return domain.Fail("SPEC_MANUAL_MIGRATION_REQUIRED", "旧 Spec 由人工迁移，不自动进入新流程")
	}
	return nil
}
