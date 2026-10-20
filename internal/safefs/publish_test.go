package safefs

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type brokenInput struct{}

func (brokenInput) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestPublishFailureDoesNotExposeFinalName(t *testing.T) {
	for _, scenario := range []string{"write", "sync"} {
		t.Run(scenario, func(t *testing.T) {
			root := publicationRoot(t)
			input := io.Reader(strings.NewReader("complete"))
			syncFile := func(f *os.File) error { return f.Sync() }
			if scenario == "write" {
				input = brokenInput{}
			} else {
				syncFile = func(*os.File) error { return io.ErrClosedPipe }
			}
			if _, err := publishFile(root, "object.gz", input, syncFile); err == nil {
				t.Fatal("injected failure must be returned")
			}
			files, err := os.ReadDir(root)
			if err != nil || len(files) != 0 {
				t.Fatalf("failed unpublished write must leave no final name or own temp: %v %v", files, err)
			}
			if created, err := PublishFile(root, "object.gz", strings.NewReader("retry")); err != nil || !created {
				t.Fatalf("retry must succeed: %v %v", created, err)
			}
		})
	}
}

func TestPublishCompetitionKeepsCompleteWinnerAndExistingEvidence(t *testing.T) {
	root := publicationRoot(t)
	if err := os.WriteFile(filepath.Join(root, ".yss-object-interrupted"), []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan bool, 2)
	errs := make(chan error, 2)
	for _, data := range []string{"first", "second"} {
		wg.Add(1)
		go func(data string) {
			defer wg.Done()
			created, err := PublishFile(root, "object.gz", strings.NewReader(data))
			results <- created
			errs <- err
		}(data)
	}
	wg.Wait()
	close(results)
	close(errs)
	winners := 0
	for created := range results {
		if created {
			winners++
		}
	}
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(root, "object.gz"))
	if err != nil || winners != 1 || string(raw) != "first" && string(raw) != "second" {
		t.Fatalf("expected exactly one complete winner: %q winners=%d %v", raw, winners, err)
	}
	before := string(raw)
	created, err := PublishFile(root, "object.gz", brokenInput{})
	if err != nil || created {
		t.Fatalf("existing evidence must not be rewritten: %v %v", created, err)
	}
	raw, _ = os.ReadFile(filepath.Join(root, "object.gz"))
	if string(raw) != before {
		t.Fatal("winner changed")
	}
	if _, err := os.Stat(filepath.Join(root, ".yss-object-interrupted")); errors.Is(err, os.ErrNotExist) {
		t.Fatal("unknown interrupted evidence must remain")
	}
}

func publicationRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}
