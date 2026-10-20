//go:build !windows

package updater

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestArchiveFIFORejectsWithoutWriter(t *testing.T) {
	base := updaterTemp(t)
	file := filepath.Join(base, "fifo.tar.gz")
	if err := unix.Mkfifo(file, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := Build(filepath.Join(base, "tool"), file, strings.Repeat("0", 64)); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO must be rejected")
		}
	case <-time.After(100 * time.Millisecond):
		// Unblock the old implementation before failing; no stranded reader remains.
		writer, err := os.OpenFile(file, os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		_ = writer.Close()
		<-done
		t.Fatal("FIFO blocked before regular-file validation")
	}
}
