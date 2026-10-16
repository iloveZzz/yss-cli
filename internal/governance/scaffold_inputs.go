package governance

import (
	"context"
	"os"
	"path/filepath"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

// VerifyScaffoldInputs runs the governance checks a backend scaffold generator
// consumes from an approved Project Scaffold Contract: schema, architecture
// decision, real user decision, design prerequisites and engineering approval.
// Platform qualification is not part of it; the generator evaluates that itself
// because it may run against a candidate catalog. It is the same code that
// `contract verify --kind scaffold` uses, so the two cannot drift. root is the
// governance project root and contractRef the contract path inside it.
func VerifyScaffoldInputs(ctx context.Context, root, contractRef string) error {
	if ctx == nil {
		return domain.Fail("ARGUMENT", "治理执行取消上下文不可为空")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	if _, err = safefs.Path(root, "yss-project.yaml"); err != nil {
		return err
	}
	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return domain.Fail("ROOT", "项目根目录必须是目录")
	}
	s := kernel.NewSemanticSession(ctx, root, map[string]string{})
	if err = s.Guard(); err != nil {
		return err
	}
	if err = s.Authorities(); err != nil {
		return err
	}
	if _, err = s.ContextContract(); err != nil {
		return err
	}
	err = s.Verify("scaffold-inputs", contractRef, map[string]string{})
	if final := s.Finish(); err == nil && final != nil {
		err = final
	}
	return err
}
