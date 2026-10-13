package main

import (
	"fmt"
	"io/fs"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Qualify a recoverable SIGKILL between published target writes. Arbitrarily
// killing a partial temporary correctly produces RECOVERY_FAILED; that refusal
// is covered separately by the transaction tests. The actual release child is
// suspended while observing, so it cannot start another write before the kill.
// Neither its journal nor any target bytes are edited by this observer.
func killAtPublishedBoundary(child *exec.Cmd, root string, deadline time.Time) error {
	defer child.Process.Kill() // Also clean up a suspended child on observer errors.
	for time.Now().Before(deadline) {
		if err := pauseChild(child); err != nil {
			return err
		}
		writing := false
		err := filepath.WalkDir(root, func(ref string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if ref == filepath.Join(root, ".yss") {
				return filepath.SkipDir
			}
			if strings.Contains(entry.Name(), ".yss-txn-") && strings.HasSuffix(entry.Name(), ".tmp") {
				writing = true
				return fs.SkipAll
			}
			return nil
		})
		if err != nil {
			return err
		}
		if !writing {
			return child.Process.Kill()
		}
		if err := resumeChild(child); err != nil {
			return err
		}
		time.Sleep(time.Millisecond)
	}
	return fmt.Errorf("no published target boundary observed before interruption deadline")
}
