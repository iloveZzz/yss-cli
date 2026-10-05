package transaction_test

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/iloveZzz/yss-cli/internal/transaction"
)

// This is a filesystem scale observation, not a benchmark of real template bytes.
// Four platform projections share about 1,107 distinct candidate objects.
func BenchmarkProfileScale4427(b *testing.B) {
	for iteration := 0; iteration < b.N; iteration++ {
		b.StopTimer()
		root, err := filepath.EvalSymlinks(b.TempDir())
		if err != nil {
			b.Fatal(err)
		}
		ops := make([]transaction.Operation, 4427)
		for i := range ops {
			logical := i / 4
			ops[i] = transaction.Operation{Path: fmt.Sprintf("platform-%d/skills/skill-%03d/references/contract-%02d.md", i%4, logical%100, logical/100), Data: []byte(fmt.Sprintf("固定模板对象 %04d: lifecycle/schema evidence\n", logical)), Mode: 0644}
		}
		b.StartTimer()
		started := time.Now()
		out, err := transaction.Apply(root, "profile-scale", ops)
		if err != nil {
			b.Fatal(err)
		}
		applied := time.Since(started)
		started = time.Now()
		if _, err := transaction.Rollback(root); err != nil {
			b.Fatal(err)
		}
		b.Logf("operations=%d apply=%s rollback=%s", out.Operations, applied, time.Since(started))
	}
}
