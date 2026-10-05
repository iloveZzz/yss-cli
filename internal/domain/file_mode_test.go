package domain

import (
	"runtime"
	"testing"
)

func TestFileModeModelsPlatformCapabilities(t *testing.T) {
	for _, entry := range []struct{ input, want uint32 }{{0755, 0644}, {0751, 0644}, {0600, 0644}, {0666, 0644}, {0444, 0444}, {0555, 0444}, {0040, 0444}, {0, 0444}} {
		if got := fileModeForOS(entry.input, "windows"); got != entry.want {
			t.Fatalf("Windows capability model %#o: got %#o want %#o", entry.input, got, entry.want)
		}
	}
	for _, mode := range []uint32{0751, 0700, 0600, 0644, 0444, 0555, 0} {
		if got := fileModeForOS(mode, "darwin"); got != mode {
			t.Fatalf("Unix bits changed: %#o -> %#o", mode, got)
		}
		if got := FileMode(mode); got != fileModeForOS(mode, runtime.GOOS) {
			t.Fatal("runtime dispatch differs")
		}
	}
}
