package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/cli"
)

func main() {
	out := flag.String("out", "docs/cli-help.md", "generated Markdown path")
	check := flag.Bool("check", false, "check the saved view without writing")
	reference := flag.Bool("reference", false, "include every command and error detail")
	embed := flag.Bool("embed", false, "replace the generated block inside an existing guide")
	flag.Parse()
	content, err := cli.HelpGuide(*reference)
	if err == nil && *embed {
		var original []byte
		original, err = os.ReadFile(*out)
		if err == nil {
			const begin = "<!-- YSS_CLI_HELP_START -->"
			const end = "<!-- YSS_CLI_HELP_END -->"
			text := string(original)
			start, finish := strings.Index(text, begin), strings.Index(text, end)
			block := begin + "\n" + content + end
			if start < 0 && finish < 0 {
				content = strings.TrimRight(text, "\n") + "\n\n" + block + "\n"
			} else if start >= 0 && finish > start {
				content = text[:start] + block + text[finish+len(end):]
			} else {
				err = fmt.Errorf("帮助块标记不完整: %s", *out)
			}
		}
	}
	if err == nil && *check {
		var actual []byte
		actual, err = os.ReadFile(*out)
		if err == nil && !bytes.Equal(actual, []byte(content)) {
			err = fmt.Errorf("帮助文档与命令元数据失配: %s", *out)
		}
	} else if err == nil {
		err = os.WriteFile(*out, []byte(content), 0644)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(*out)
}
