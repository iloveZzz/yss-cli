package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/iloveZzz/yss-cli/internal/bundle"
	"os"
)

func main() {
	root := flag.String("source-root", "", "template repository worktree")
	out := flag.String("out", "internal/bundle/assets", "embedded archive directory")
	flag.Parse()
	if *root == "" || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: go run -tags yss_nobundle ./tools/bundle --source-root ROOT --out OUT")
		os.Exit(1)
	}
	b, err := bundle.BuildRoot(context.Background(), *root)
	if err == nil {
		var report *bundle.StorageReport
		report, err = bundle.WriteBuilt(*out, b)
		if err == nil {
			raw, _ := json.MarshalIndent(report, "", "  ")
			fmt.Println(string(raw))
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
