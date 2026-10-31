package transaction

import (
	"path/filepath"
	"strings"
)

func retiredDisjoint(l loaded, records []record) bool {
	switch l.plan.Kind {
	case "init", "attach", "sync", "skills", "program-update":
		return false
	}
	for _, old := range l.plan.Operations {
		for _, next := range records {
			if old.Path == next.Path || strings.HasPrefix(old.Path, next.Path+"/") || strings.HasPrefix(next.Path, old.Path+"/") {
				return false
			}
		}
	}
	return true
}

// CheckPending performs the same path and pending-transaction preflight as apply,
// without creating a lock, output directory or saved plan.
func CheckPending(root string, ops []Operation) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	records, err := normalize(root, ops)
	if err != nil {
		return err
	}
	all, preparations, err := scan(root)
	if err != nil {
		return err
	}
	if len(preparations) > 0 {
		return fail("INTERRUPTED", "存在未完成准备记录；使用 doctor 查看或 recover")
	}
	for _, l := range all {
		if !terminal(l.journal.Phase) && !retiredDisjoint(l, records) {
			return fail("INTERRUPTED", "存在与本次操作相关的未完成事务: "+l.plan.ID+" ("+l.plan.Kind+")")
		}
	}
	return nil
}
