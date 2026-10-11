package governance

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Only test fixture producers need the explicit native source-root adapter.
// The fixed oracle checkout and all production validators remain unchanged.
func governanceNodeCommand(t *testing.T, args ...string) *governanceTestCommand {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate native fixture adapter")
	}
	adapter := filepath.Join(filepath.Dir(file), "..", "..", "tools", "native-fixture-register.mjs")
	return governanceCommand(t, "node", append([]string{"--import", adapter}, args...)...)
}

type governanceTestCommand struct {
	*exec.Cmd
	t *testing.T
}

func (c *governanceTestCommand) CombinedOutput() ([]byte, error) {
	started := time.Now()
	label := strings.Join(c.Args, " ")
	if len(label) > 180 {
		label = label[:180] + "…"
	}
	c.t.Logf("subprocess start: %s", label)
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				c.t.Logf("subprocess running %.0fs: %s", time.Since(started).Seconds(), label)
			}
		}
	}()
	raw, err := c.Cmd.CombinedOutput()
	close(done)
	<-stopped
	c.t.Logf("subprocess finished %.3fs: %s (error=%v)", time.Since(started).Seconds(), label, err)
	return raw, err
}

func governanceCommand(t *testing.T, command string, args ...string) *governanceTestCommand {
	t.Helper()
	limit := 10 * time.Minute
	// The complete Spec/Design/business producer exceeded 10 minutes in the
	// measured full run. Keep its budget explicit, below the package deadline.
	if command == "node" && strings.Split(t.Name(), "/")[0] == "TestLifecycleTargetNativeSpecDesignBusinessSequence" {
		limit = 20 * time.Minute
	}
	deadline := time.Now().Add(limit)
	if end, ok := t.Deadline(); ok && end.Add(-5*time.Second).Before(deadline) {
		deadline = end.Add(-5 * time.Second)
	}
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.WaitDelay = 2 * time.Second
	configureTestProcess(cmd)
	return &governanceTestCommand{Cmd: cmd, t: t}
}
