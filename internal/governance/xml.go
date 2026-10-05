package governance

import (
	"bytes"
	"encoding/xml"
	"io"
	"os"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func xmlRun(action, root string, args map[string]string) (any, error) {
	if action != "inspect" && action != "query" {
		return nil, domain.Fail("UNPORTED", "XML 动作尚未迁移: "+action)
	}
	ref := first(args["file"], args["arg0"])
	p, err := safefs.Path(root, ref)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	st, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > 16*1024*1024 {
		return nil, domain.Fail("XML_LIMIT", "XML 必须是小于 16MiB 的普通文件")
	}
	b, err := io.ReadAll(io.LimitReader(file, 16*1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 16*1024*1024 {
		return nil, domain.Fail("XML_LIMIT", "XML 内容大小发生漂移")
	}
	decoder := xml.NewDecoder(bytes.NewReader(b))
	decoder.Strict = true
	stack := []xml.Name{}
	rootCount := 0
	artifact := ""
	deps := []string{}
	modules := []string{}
	capture := ""
	value := ""
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, domain.Wrap("XML", err)
		}
		switch t := token.(type) {
		case xml.Directive:
			return nil, domain.Fail("XML", "禁止 DTD 与 XML 指令，不读取外部实体")
		case xml.StartElement:
			if len(stack) == 0 {
				rootCount++
				if rootCount != 1 || t.Name.Local != "project" || (t.Name.Space != "" && t.Name.Space != "http://maven.apache.org/POM/4.0.0") {
					return nil, domain.Fail("XML", "XML 必须包含唯一 Maven project 根")
				}
			}
			if len(stack) >= 128 {
				return nil, domain.Fail("XML_LIMIT", "XML 嵌套深度超限")
			}
			if capture != "" {
				return nil, domain.Fail("XML", "Maven 构建值必须是纯文本")
			}
			stack = append(stack, t.Name)
			names := make([]string, len(stack))
			validNamespace := true
			for i, n := range stack {
				names[i] = n.Local
				if n.Space != "" && n.Space != "http://maven.apache.org/POM/4.0.0" {
					validNamespace = false
				}
			}
			path := strings.Join(names, "/")
			if validNamespace {
				if path == "project/artifactId" {
					capture = "artifact"
				} else if path == "project/dependencies/dependency/artifactId" || path == "project/profiles/profile/dependencies/dependency/artifactId" {
					capture = "dependency"
				} else if len(names) >= 2 && names[len(names)-2] == "modules" && names[len(names)-1] == "module" {
					capture = "module"
				}
			}
			value = ""
		case xml.CharData:
			if len(stack) == 0 && strings.TrimSpace(string(t)) != "" {
				return nil, domain.Fail("XML", "根元素外存在非空文本")
			}
			if capture != "" {
				value += string(t)
			}
		case xml.EndElement:
			if capture != "" {
				value = strings.TrimSpace(value)
				switch capture {
				case "artifact":
					if artifact != "" {
						return nil, domain.Fail("XML", "artifactId 重复")
					}
					artifact = value
				case "dependency":
					if value != "" {
						deps = append(deps, value)
					}
				case "module":
					if value != "" {
						modules = append(modules, value)
					}
				}
				capture = ""
				value = ""
			}
			if len(stack) == 0 {
				return nil, domain.Fail("XML", "XML 栈无效")
			}
			stack = stack[:len(stack)-1]
		}
	}
	if rootCount != 1 || len(stack) != 0 {
		return nil, domain.Fail("XML", "XML project 不完整")
	}
	return map[string]any{"file": ref, "file_digest": "sha256:" + safefs.Digest(b), "artifact_id": artifact, "dependencies": deps, "modules": modules, "read_only": true, "commands_executed": false, "execution_authorization": "not-evaluated", "scope": "maven-xml-only"}, nil
}
