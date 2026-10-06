package project

import (
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"os"
	"path/filepath"
)

// Retain the existing historical-state refusal contract and bind observed
// journal bytes to the saved native migration plan. The old executor remains
// responsible for interpreting or restoring those journals.
func legacyRecoveryInputs(id *Identity, inputs map[string]domain.Descriptor) error {
	if err := CheckLegacyState(id.Root, id.Profile.Name); err != nil {
		return err
	}
	refs := []string{".yss-harness-migrate.lock", ".yss-harness-state/lock.json", ".yss-harness-state/upgrade.json", ".yss-harness-state/owner.json"}
	for _, family := range []string{"spec", "design", "backend", "frontend"} {
		dirRef := ".yss-harness-state/" + family + "/transactions"
		dir, err := safefs.Path(id.Root, dirRef)
		if err != nil {
			return err
		}
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				return domain.Fail("STATE", "未知旧事务目录")
			}
			refs = append(refs, filepath.ToSlash(filepath.Join(dirRef, entry.Name(), "journal.json")))
		}
	}
	for _, ref := range refs {
		d, err := safefs.Describe(id.Root, ref)
		if err != nil {
			return err
		}
		inputs[ref] = d
	}
	return nil
}
