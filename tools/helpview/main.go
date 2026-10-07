package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/iloveZzz/yss-cli/internal/helpview"
)

func main() {
	check := flag.Bool("check", false, "只读核验视图与固定 Bundle 一致")
	out := flag.String("out", "internal/helpview/assets", "派生帮助视图目录")
	flag.Parse()
	for _, profile := range []string{"spec", "design", "backend", "frontend"} {
		v, e := helpview.Derive(profile)
		if e != nil {
			panic(e)
		}
		raw, e := json.MarshalIndent(v, "", "  ")
		if e != nil {
			panic(e)
		}
		raw = append(raw, '\n')
		file := filepath.Join(*out, profile+".json")
		if *check {
			actual, e := os.ReadFile(file)
			if e != nil || !bytes.Equal(actual, raw) {
				fmt.Fprintln(os.Stderr, "帮助视图漂移:", file)
				os.Exit(1)
			}
		} else {
			if e = os.MkdirAll(*out, 0755); e != nil {
				panic(e)
			}
			if e = os.WriteFile(file, raw, 0644); e != nil {
				panic(e)
			}
		}
		fmt.Printf("%s: %d stages, template=%s, registry=%s\n", profile, len(v.Stages), v.TemplateCommit, v.RegistrySHA256)
	}
}
