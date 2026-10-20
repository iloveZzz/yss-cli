// docstatus keeps current documentation facts tied to their production sources.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
)

const begin = "<!-- YSS_CURRENT_STATUS_START -->"
const end = "<!-- YSS_CURRENT_STATUS_END -->"

func main() {
	check := flag.Bool("check", false, "只读检查当前版本、模板与 Profile 文档")
	flag.Parse()
	if err := run(*check); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(check bool) error {
	raw, err := os.ReadFile("docs/source-lock.json")
	if err != nil {
		return err
	}
	var lock struct {
		Profiles map[string]struct {
			Tag    string `json:"templateTag"`
			Commit string `json:"templateCommit"`
		} `json:"profiles"`
	}
	if err = json.Unmarshal(raw, &lock); err != nil {
		return err
	}
	names := []string{}
	for name := range domain.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	templates, profiles := []string{}, []string{}
	if len(lock.Profiles) != len(names) {
		return fmt.Errorf("来源锁与现役 Profile 清单不一致")
	}
	for _, name := range names {
		source, ok := lock.Profiles[name]
		if !ok || source.Tag == "" || len(source.Commit) != 40 {
			return fmt.Errorf("%s 缺少固定 tag/commit", name)
		}
		templates = append(templates, fmt.Sprintf("%s `%s`", name, source.Tag))
		profiles = append(profiles, "`"+name+"`")
	}
	block := fmt.Sprintf("%s\n当前源码版本：`%s`。固定模板：%s。现役 Profile：%s。协议版本：`%d`。\n%s", begin, domain.Version, strings.Join(templates, "、"), strings.Join(profiles, "、"), domain.ProtocolVersion, end)
	for _, file := range []string{"README.md", "docs/porting-status.md", "docs/ci-verification.md"} {
		original, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		text := string(original)
		start, finish := strings.Index(text, begin), strings.Index(text, end)
		var updated string
		if start < 0 && finish < 0 {
			if check {
				return fmt.Errorf("缺少当前事实生成块: %s", file)
			}
			at := strings.Index(text, "\n\n")
			if at < 0 {
				return fmt.Errorf("文档缺少标题段落: %s", file)
			}
			updated = text[:at+2] + block + "\n\n" + text[at+2:]
		} else if start >= 0 && finish > start && strings.Count(text, begin) == 1 && strings.Count(text, end) == 1 {
			updated = text[:start] + block + text[finish+len(end):]
		} else {
			return fmt.Errorf("当前事实标记不完整或重复: %s", file)
		}
		if check {
			if updated != text {
				return fmt.Errorf("当前事实漂移: %s；运行 go run ./tools/docstatus", file)
			}
		} else if err = os.WriteFile(file, []byte(updated), 0644); err != nil {
			return err
		}
	}
	fmt.Println("当前版本、模板与 Profile 文档一致")
	return nil
}
