// bundle produces deterministic embedded assets from four fixed Git commits.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"os"
	"path/filepath"
)

func main() {
	if e := run(context.Background(), os.Args[1:]); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run(ctx context.Context, args []string) error {
	f := flag.NewFlagSet("bundle", flag.ContinueOnError)
	sourceRoot := f.String("source-root", "", "root containing the four Git template repositories")
	lockPath := f.String("lock", "docs/source-lock.json", "source lock v2/v3")
	out := f.String("out", "internal/bundle/assets", "output shared embedded archive directory")
	if e := f.Parse(args); e != nil {
		return e
	}
	if f.NArg() != 0 || *sourceRoot == "" {
		return fmt.Errorf("usage: go run ./tools/bundle --source-root ROOT --lock LOCK --out OUT")
	}
	raw, e := os.ReadFile(*lockPath)
	if e != nil {
		return e
	}
	var lock bundle.SourceLock
	if e = json.Unmarshal(raw, &lock); e != nil {
		return e
	}
	root, e := filepath.Abs(*sourceRoot)
	if e != nil {
		return e
	}
	for key, source := range lock.Profiles {
		if source.SourcePath == "." {
			source.Root = root
		} else {
			source.Root, e = safefs.Path(root, source.SourcePath)
			if e != nil {
				return e
			}
		}
		if source.SkillsSource != nil {
			if source.SkillsSource.SourcePath == "." {
				source.SkillsSource.Root = root
			} else {
				source.SkillsSource.Root, e = safefs.Path(root, source.SkillsSource.SourcePath)
				if e != nil {
					return e
				}
			}
		}
		lock.Profiles[key] = source
	}
	bundles, e := bundle.BuildLock(ctx, lock)
	if e != nil {
		return e
	}
	storage, e := bundle.WriteBuilt(*out, bundles)
	if e != nil {
		return e
	}
	result := map[string]any{"schemaVersion": 4, "profiles": map[string]any{}, "storage": storage}
	profiles := result["profiles"].(map[string]any)
	for key, b := range bundles {
		profiles[key] = map[string]any{"templateCommit": b.TemplateCommit, "templateVersion": b.TemplateVersion, "sourceState": b.SourceState, "sourceSnapshotHash": b.SnapshotHash, "manifestHash": b.ManifestHash, "bundleHash": b.BundleHash, "sourcePolicy": b.SourcePolicy, "files": len(b.Files), "initialFiles": len(b.Initial)}
	}
	data, e := json.MarshalIndent(result, "", "  ")
	if e != nil {
		return e
	}
	fmt.Println(string(data))
	return nil
}
