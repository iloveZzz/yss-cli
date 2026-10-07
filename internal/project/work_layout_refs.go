package project

import (
	"bytes"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"go.yaml.in/yaml/v3"
)

type workDocument struct {
	value    any
	original any
	raw      []byte
	format   string
	node     yaml.Node
	body     []byte
	markdown bool
}

var workFrontmatter = regexp.MustCompile(`(?s)^---\r?\n(.*?)\r?\n---(?:\r?\n|$)(.*)$`)

func parseWorkDocument(raw []byte, ext string) (*workDocument, error) {
	d := &workDocument{raw: raw, format: strings.ToLower(ext)}
	header := raw
	if d.format == ".md" {
		if !bytes.HasPrefix(raw, []byte("---\n")) && !bytes.HasPrefix(raw, []byte("---\r\n")) {
			return nil, nil
		}
		parts := workFrontmatter.FindSubmatch(raw)
		if parts == nil {
			return nil, domain.Fail("WORK_LAYOUT_REFERENCE", "frontmatter 不完整")
		}
		header = parts[1]
		d.body = parts[2]
		d.markdown = true
	} else if d.format != ".json" && d.format != ".yaml" && d.format != ".yml" {
		return nil, nil
	}
	v, e := schema.Parse(header)
	if e != nil {
		return nil, e
	}
	d.value = v
	d.original, e = schema.Parse(header)
	if e != nil {
		return nil, e
	}
	if d.format != ".json" {
		if e = yaml.Unmarshal(header, &d.node); e != nil {
			return nil, e
		}
	}
	return d, nil
}
func (d *workDocument) encode() ([]byte, error) {
	old, _ := jsonBytes(d.original)
	current, e := jsonBytes(d.value)
	if e != nil {
		return nil, e
	}
	if bytes.Equal(old, current) {
		if !d.markdown {
			return d.raw, nil
		}
		parts := workFrontmatter.FindSubmatchIndex(d.raw)
		return append(append([]byte(nil), d.raw[:parts[4]]...), d.body...), nil
	}
	if d.format == ".json" {
		return current, nil
	}
	workSyncNode(&d.node, d.value)
	b, e := yaml.Marshal(&d.node)
	if e != nil {
		return nil, e
	}
	if d.markdown {
		b = append(append(append([]byte("---\n"), b...), []byte("---\n")...), d.body...)
	}
	return b, nil
}

// Update existing scalar nodes so comments, order and quoting survive.
func workSyncNode(node *yaml.Node, v any) {
	if node.Kind == yaml.DocumentNode {
		workSyncNode(node.Content[0], v)
		return
	}
	switch x := v.(type) {
	case map[string]any:
		seen := map[string]bool{}
		for i := 0; i+1 < len(node.Content); i += 2 {
			k := node.Content[i].Value
			seen[k] = true
			workSyncNode(node.Content[i+1], x[k])
		}
		keys := []string{}
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			value := x[k]
			if !seen[k] {
				key := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k}
				n := &yaml.Node{}
				_ = n.Encode(value)
				node.Content = append(node.Content, key, n)
			}
		}
	case []any:
		for i, value := range x {
			if i < len(node.Content) {
				workSyncNode(node.Content[i], value)
			} else {
				n := &yaml.Node{}
				_ = n.Encode(value)
				node.Content = append(node.Content, n)
			}
		}
		if len(node.Content) > len(x) {
			node.Content = node.Content[:len(x)]
		}
	case string:
		if node.Value != x {
			node.Value = x
			node.Tag = "!!str"
		}
	default:
		// Keep unchanged scalars in their original representation.
	}
}
func workReferenceKey(key string) bool {
	return key == "ref" || key == "$ref" || key == "path" || key == "paths" || key == "refs" || strings.HasSuffix(key, "_ref") || strings.HasSuffix(key, "_refs") || strings.HasSuffix(key, "_path") || strings.HasSuffix(key, "_paths")
}
func workMappedReference(ref string, mapping map[string]string) (string, bool) {
	parts := strings.SplitN(ref, "#", 2)
	file := strings.TrimSuffix(parts[0], "/")
	target, ok := mapping[file]
	if !ok {
		return ref, false
	}
	if strings.HasSuffix(parts[0], "/") {
		target += "/"
	}
	if len(parts) > 1 {
		target += "#" + parts[1]
	}
	return target, true
}
func workProtectedPath(ref string) bool {
	return strings.Contains(ref, "/user-decisions/") || strings.Contains(ref, "/verification/") || strings.Contains(ref, "/gates/") || strings.Contains(path.Base(ref), "user-decision") || strings.Contains(path.Base(ref), "approval") || strings.Contains(path.Base(ref), "freeze-record")
}
func workAffectedReference(v any, mapping map[string]string, key string) bool {
	if workHistoricalKey(key) {
		return false
	}
	switch x := v.(type) {
	case string:
		_, ok := workMappedReference(x, mapping)
		return ok
	case []any:
		for _, a := range x {
			if workAffectedReference(a, mapping, key) {
				return true
			}
		}
	case map[string]any:
		for k, a := range x {
			if workAffectedReference(a, mapping, k) {
				return true
			}
		}
	}
	return false
}
func workChangedBasis(v any, mapping map[string]string, candidates, original map[string][]byte, key string) bool {
	if workHistoricalKey(key) {
		return false
	}
	switch x := v.(type) {
	case string:
		if workReferenceKey(key) {
			x = strings.TrimSuffix(strings.SplitN(x, "#", 2)[0], "/")
			dest := x
			if to, ok := mapping[x]; ok {
				dest = to
			}
			before, ok := original[x]
			after, found := candidates[dest]
			return ok && found && !bytes.Equal(before, after)
		}
	case []any:
		for _, a := range x {
			if workChangedBasis(a, mapping, candidates, original, key) {
				return true
			}
		}
	case map[string]any:
		for k, a := range x {
			if workChangedBasis(a, mapping, candidates, original, k) {
				return true
			}
		}
	}
	return false
}
func workCheckReferences(v any, candidates map[string][]byte, inputs map[string]domain.Descriptor, mapping map[string]string, owner, source, key string, block func(string, string)) {
	if workHistoricalKey(key) {
		return
	}
	switch x := v.(type) {
	case string:
		if strings.Contains(x, source+"/") {
			block("UNKNOWN_REFERENCE", "未识别的当前引用: "+x)
			return
		}
		if !workReferenceKey(key) {
			return
		}
		ref := strings.TrimSuffix(strings.SplitN(x, "#", 2)[0], "/")
		if key == "$ref" && ref != "" && !strings.HasPrefix(ref, ".work/") && !strings.HasPrefix(ref, ".template-spec/") {
			if strings.Contains(ref, ":") || strings.HasPrefix(ref, "/") {
				block("EXTERNAL_REFERENCE", "无法隔离核验外部引用: "+x)
				return
			}
			ref = path.Clean(path.Join(path.Dir(owner), ref))
			if e := safefs.ValidateRef(ref); e != nil {
				block("UNSAFE_REFERENCE", e.Error())
				return
			}
		} else if !strings.HasPrefix(ref, ".work/") {
			return
		}
		if inputs[ref].Type == "file" || inputs[ref].Type == "directory" {
			return
		}
		if _, ok := candidates[ref]; ok {
			return
		}
		for from, to := range mapping {
			if to == ref && (inputs[from].Type == "file" || inputs[from].Type == "directory") {
				return
			}
		}
		block("MISSING_REFERENCE", "缺少当前引用: "+x)
	case []any:
		for _, a := range x {
			workCheckReferences(a, candidates, inputs, mapping, owner, source, key, block)
		}
	case map[string]any:
		for k, a := range x {
			workCheckReferences(a, candidates, inputs, mapping, owner, source, k, block)
		}
	}
}

var workLink = regexp.MustCompile(`(!?\[[^\]\n]*\]\()(<)?([^\s)>]+)(>?)([^)]*\))`)
var workCodeRef = regexp.MustCompile("`([^`\\n]+)`")

func workCurrentMarkdown(raw []byte, ref string, inputs map[string]domain.Descriptor) error {
	for _, link := range workLink.FindAllStringSubmatch(string(raw), -1) {
		file := strings.SplitN(link[3], "#", 2)[0]
		if file == "" || strings.Contains(file, ":") {
			continue
		}
		if !strings.HasPrefix(file, ".work/") {
			file = path.Clean(path.Join(path.Dir(ref), file))
		}
		file = strings.TrimSuffix(file, "/")
		if strings.HasPrefix(file, ".work/") && inputs[file].Type != "file" && inputs[file].Type != "directory" {
			return domain.Fail("MISSING_REFERENCE", "缺少当前 Markdown 引用: "+file)
		}
	}
	return nil
}

func workMarkdown(raw []byte, ref string, mapping map[string]string, inputs map[string]domain.Descriptor, source string, block func(string, string)) []byte {
	resolve := func(dest string, relative bool) string {
		parts := strings.SplitN(dest, "#", 2)
		file := parts[0]
		anchor := ""
		if len(parts) > 1 {
			anchor = "#" + parts[1]
		}
		if file == "" || strings.Contains(file, ":") {
			if strings.Contains(file, source+"/") {
				block("EXTERNAL_REFERENCE", "外部引用需人工处理: "+dest)
			}
			return dest
		}
		lookup := file
		if !strings.HasPrefix(file, source+"/") && !strings.HasPrefix(file, ".work/") && relative {
			lookup = path.Clean(path.Join(path.Dir(ref), file))
		}
		to, found := mapping[lookup]
		if !found {
			if strings.HasPrefix(lookup, source+"/") {
				block("MISSING_REFERENCE", "缺少 Markdown 引用: "+dest)
			}
			return dest
		}
		if relative && lookup != file {
			from := ref
			if moved, ok := mapping[ref]; ok {
				from = moved
			}
			rel, e := filepath.Rel(filepath.FromSlash(path.Dir(from)), filepath.FromSlash(to))
			if e == nil {
				to = filepath.ToSlash(rel)
			}
		}
		return to + anchor
	}
	s := workLink.ReplaceAllStringFunc(string(raw), func(link string) string {
		parts := workLink.FindStringSubmatch(link)
		return parts[1] + parts[2] + resolve(parts[3], true) + parts[4] + parts[5]
	})
	s = workCodeRef.ReplaceAllStringFunc(s, func(token string) string {
		file := token[1 : len(token)-1]
		if strings.HasPrefix(file, source+"/") {
			return "`" + resolve(file, false) + "`"
		}
		return token
	})
	if strings.Contains(s, source+"/") {
		block("UNKNOWN_REFERENCE", "无法确定的文本或动态引用，须明确修复后重新规划")
	}
	return []byte(s)
}
func workGitIdentity(root string) (string, error) {
	if _, e := os.Lstat(filepath.Join(root, ".git")); os.IsNotExist(e) {
		return "", nil
	}
	var out bytes.Buffer
	for _, args := range [][]string{{"rev-parse", "--verify", "HEAD"}, {"symbolic-ref", "--quiet", "HEAD"}} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		b, e := cmd.Output()
		if e == nil {
			out.Write(b)
		}
		out.WriteByte(0)
	}
	b, e := exec.Command("git", "-C", root, "rev-parse", "--git-path", "index").Output()
	if e != nil {
		return "", e
	}
	file := strings.TrimSpace(string(b))
	if !filepath.IsAbs(file) {
		file = filepath.Join(root, file)
	}
	b, e = os.ReadFile(file)
	if e != nil && !os.IsNotExist(e) {
		return "", e
	}
	out.Write(b)
	return safefs.Digest(out.Bytes()), nil
}
