// docstatus keeps current documentation facts tied to their production sources.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
)

const begin = "<!-- YSS_CURRENT_STATUS_START -->"
const end = "<!-- YSS_CURRENT_STATUS_END -->"

type templateSource struct {
	Tag     string `json:"templateTag"`
	Commit  string `json:"templateCommit"`
	Version string `json:"templateVersion"`
	State   string `json:"sourceState"`
}

func templateLabel(source templateSource) (string, error) {
	if !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(source.Commit) {
		return "", fmt.Errorf("需要完整固定 commit")
	}
	if source.Tag != "" {
		return source.Tag, nil
	}
	if source.State == "committed" && source.Version == "git:"+source.Commit {
		return source.Version, nil
	}
	return "", fmt.Errorf("无 tag 来源须使用 committed 状态与匹配的 git:commit 版本")
}

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
		Profiles map[string]templateSource `json:"profiles"`
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
		label, labelErr := templateLabel(source)
		if !ok || labelErr != nil {
			return fmt.Errorf("%s 缺少有效固定模板来源: %v", name, labelErr)
		}
		templates = append(templates, fmt.Sprintf("%s `%s`", name, label))
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
