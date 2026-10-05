package governance_test

import (
	"archive/zip"
	"context"
	"database/sql"
	"github.com/iloveZzz/yss-cli/internal/governance"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRuntimeCancellationUnderExclusiveLockDoesNotPersistEvent(t *testing.T) {
	root, home := canonicalTemp(t), canonicalTemp(t)
	v, err := governance.Run("runtime", "begin", root, map[string]string{"home": home})
	if err != nil {
		t.Fatal(err)
	}
	m := v.(map[string]any)
	db, err := sql.Open("sqlite", filepath.Join(m["directory"].(string), "runtime.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err = db.Exec("BEGIN EXCLUSIVE"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, e := governance.RunContext(ctx, "runtime", "event", root, map[string]string{"home": home, "id": m["id"].(string), "token": m["token"].(string), "type": "cancelled", "value": "null"})
		done <- e
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	if _, err = db.Exec("COMMIT"); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if err == nil {
			t.Fatal("cancelled event reported success")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancelled request did not return")
	}
	var n int
	if err = db.QueryRow("SELECT count(*) FROM events").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("cancelled event persisted: %d", n)
	}
}
func TestArchivePortableNamesAndPrefixAliases(t *testing.T) {
	for _, names := range [][]string{{"CON.txt"}, {"trailing."}, {"bad?.txt"}, {"Docs/a.txt", "docs/b.txt"}, {"Straße/a", "strasse/b"}} {
		t.Run(names[0], func(t *testing.T) {
			root := canonicalTemp(t)
			f, err := os.Create(filepath.Join(root, "unsafe.zip"))
			if err != nil {
				t.Fatal(err)
			}
			w := zip.NewWriter(f)
			for _, name := range names {
				h := zip.FileHeader{Name: name}
				h.SetMode(0644)
				e, err := w.CreateHeader(&h)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = e.Write([]byte("data")); err != nil {
					t.Fatal(err)
				}
			}
			if err = w.Close(); err != nil {
				t.Fatal(err)
			}
			if err = f.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err = governance.Run("archive", "verify", root, map[string]string{"file": "unsafe.zip"}); err == nil {
				t.Fatalf("unsafe names accepted: %v", names)
			}
		})
	}
}

// Deterministically deliver real cancellation immediately after the entry check.
// The next Context.Err observes the closed Done channel.
type cancelAfterEntry struct {
	context.Context
	cancel  context.CancelFunc
	checked bool
}

func (c *cancelAfterEntry) Err() error {
	if !c.checked {
		c.checked = true
		c.cancel()
		return nil
	}
	return c.Context.Err()
}
func TestArchiveCancellationAfterEntryDoesNotCreateOutput(t *testing.T) {
	for _, action := range []string{"pack", "unpack"} {
		t.Run(action, func(t *testing.T) {
			root := canonicalTemp(t)
			put(t, root, "input/asset.txt", "data")
			source := "input"
			if action == "unpack" {
				f, err := os.Create(filepath.Join(root, "input.zip"))
				if err != nil {
					t.Fatal(err)
				}
				w := zip.NewWriter(f)
				e, err := w.Create("asset.txt")
				if err != nil {
					t.Fatal(err)
				}
				if _, err = e.Write([]byte("data")); err != nil {
					t.Fatal(err)
				}
				if err = w.Close(); err != nil {
					t.Fatal(err)
				}
				if err = f.Close(); err != nil {
					t.Fatal(err)
				}
				source = "input.zip"
			}
			output := filepath.Join(canonicalTemp(t), "new-output")
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := &cancelAfterEntry{Context: base, cancel: cancel}
			if _, err := governance.RunContext(ctx, "archive", action, root, map[string]string{"source": source, "output": output}); err == nil {
				t.Fatal("cancelled archive reported success")
			}
			if _, err := os.Stat(output); !os.IsNotExist(err) {
				t.Fatal("cancelled archive created output")
			}
		})
	}
}
