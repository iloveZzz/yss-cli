package reading

import (
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
)

// PolicyRef 是阅读策略文件的项目内位置。
const PolicyRef = readingPolicyRef

// PolicyDoc 是启用后的策略内容，字段顺序即计划里 policy_after 的输出顺序。
type PolicyDoc struct {
	SchemaVersion int      `json:"schema_version"`
	Mode          string   `json:"mode"`
	Checkpoints   []string `json:"checkpoints"`
}

// EnablePolicy 计算把 checkpoint 加入 managed 策略后的策略文件字节。
// 键序沿用现有文件，缺失则用 schema 的固定序；序列化与 Node `JSON.stringify(x, null, 2) + "\n"` 一致。
// present 为 false 表示策略文件当前不存在。
func EnablePolicy(s *kernel.SemanticSession, cpRef string) (after []byte, doc PolicyDoc, before []byte, present bool, err error) {
	policy, present, err := loadReadingPolicy(s)
	if err != nil {
		return nil, PolicyDoc{}, nil, false, err
	}
	order := []string{"schema_version", "mode", "checkpoints"}
	checkpoints := []string{}
	if present {
		if before, err = s.Bytes(readingPolicyRef); err != nil {
			return nil, PolicyDoc{}, nil, false, err
		}
		node, perr := readingParse(before)
		if perr != nil {
			return nil, PolicyDoc{}, nil, false, s.Unavailable("INPUT", perr.Error())
		}
		order = order[:0]
		for _, p := range node.Pairs {
			order = append(order, p.Key)
		}
		for _, item := range kernel.SemList(policy["checkpoints"]) {
			checkpoints = append(checkpoints, kernel.Text(item))
		}
	}
	seen := map[string]bool{}
	for _, c := range append(checkpoints, cpRef) {
		seen[c] = true
	}
	checkpoints = checkpoints[:0]
	for c := range seen {
		checkpoints = append(checkpoints, c)
	}
	// JavaScript 默认 sort 按 UTF-16 码元比较。
	sort.Slice(checkpoints, func(i, j int) bool { return less16(checkpoints[i], checkpoints[j]) })
	doc = PolicyDoc{SchemaVersion: 1, Mode: "managed", Checkpoints: checkpoints}
	var out strings.Builder
	out.WriteString("{\n")
	for i, key := range order {
		out.WriteString("  " + jsString(key) + ": ")
		switch key {
		case "schema_version":
			out.WriteString("1")
		case "mode":
			out.WriteString(jsString("managed"))
		case "checkpoints":
			if len(checkpoints) == 0 {
				out.WriteString("[]")
			} else {
				out.WriteString("[\n")
				for j, c := range checkpoints {
					out.WriteString("    " + jsString(c))
					if j+1 < len(checkpoints) {
						out.WriteString(",")
					}
					out.WriteString("\n")
				}
				out.WriteString("  ]")
			}
		default:
			return nil, PolicyDoc{}, nil, false, s.Unavailable("INPUT", "阅读策略含未知字段: "+key)
		}
		if i+1 < len(order) {
			out.WriteString(",")
		}
		out.WriteString("\n")
	}
	out.WriteString("}\n")
	return []byte(out.String()), doc, before, present, nil
}

func less16(a, b string) bool {
	x, y := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
	for i := 0; i < len(x) && i < len(y); i++ {
		if x[i] != y[i] {
			return x[i] < y[i]
		}
	}
	return len(x) < len(y)
}
