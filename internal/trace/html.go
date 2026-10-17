package trace

import (
	"bytes"
	"errors"
	"html/template"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
)

var page = template.Must(template.New("trace").Funcs(template.FuncMap{
	"join": func(v []string) string { return strings.Join(v, ", ") },
	"short": func(v []string) string {
		out := []string{}
		for _, s := range v {
			if len(s) > 10 {
				s = s[:10]
			}
			out = append(out, s)
		}
		return strings.Join(out, ", ")
	},
	"gap": func(v []string) bool { return len(v) == 0 },
}).Parse(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>追溯矩阵 {{.Feature}}</title>
<style>
:root{--bg:#fff;--fg:#1f2328;--muted:#59636e;--line:#d1d9e0;--gap:#fff1e5;--gapfg:#9a3412}
@media (prefers-color-scheme:dark){:root{--bg:#0d1117;--fg:#e6edf3;--muted:#9198a1;--line:#3d444d;--gap:#3b2300;--gapfg:#ffb77c}}
body{background:var(--bg);color:var(--fg);font:14px/1.5 system-ui,sans-serif;margin:0;padding:16px}
h1{font-size:20px}h2{font-size:16px;margin-top:24px}.muted{color:var(--muted)}
.wrap{overflow-x:auto}table{border-collapse:collapse;min-width:640px;width:100%}
th,td{border:1px solid var(--line);padding:6px 8px;text-align:left;vertical-align:top}
td.gap{background:var(--gap);color:var(--gapfg)}code{font-size:12px}
</style></head><body>
<h1>追溯矩阵：{{.Feature}}</h1>
<p class="muted">状态：{{.Status}}。只读汇总现有资产，不代表批准、完成或可发布。</p>
<div class="wrap"><table><thead><tr><th>验收</th><th>需求</th><th>业务票</th><th>切片</th><th>验证</th><th>提交</th></tr></thead><tbody>
{{range .Matrix}}<tr><td><code>{{.Acceptance}}</code></td><td>{{join .Requirements}}</td><td>{{join .BusinessTickets}}</td>
<td{{if gap .Slices}} class="gap"{{end}}>{{if gap .Slices}}无切片{{else}}{{join .Slices}}{{end}}</td>
<td{{if gap .Verifications}} class="gap"{{end}}>{{if gap .Verifications}}无验证{{else}}{{join .Verifications}}{{end}}</td>
<td><code>{{short .Commits}}</code></td></tr>
{{end}}</tbody></table></div>
<h2>缺口</h2><ul>
<li>无切片的验收：{{join .Gaps.AcceptanceWithoutSlice}}</li>
<li>无验证的验收：{{join .Gaps.AcceptanceWithoutTest}}</li>
<li>无 Ticket 的提交：<code>{{short .Gaps.CommitsWithoutTicket}}</code></li>
<li>Ticket 未对应切片的提交：<code>{{short .Gaps.CommitsUnmapped}}</code></li>
<li>依据摘要已变化：{{range .Gaps.BasisChanged}}<code>{{.Ref}}</code>（{{.Key}}） {{end}}</li>
</ul></body></html>
`))

// WriteHTML 把矩阵写成新文件；目标须在项目外且不存在，报告不改变它所描述的项目。
func WriteHTML(root, out string, r *Report) error {
	abs, err := filepath.Abs(out)
	if err != nil {
		return err
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return domain.Fail("PATH", "报告所在目录不存在: "+filepath.Dir(abs))
	}
	top, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	if dir == top || strings.HasPrefix(dir+string(filepath.Separator), top+string(filepath.Separator)) {
		return domain.Fail("PATH", "HTML 报告须写在项目之外")
	}
	var buf bytes.Buffer
	if err = page.Execute(&buf, r); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, filepath.Base(abs)), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return domain.Fail("EXISTS", "报告文件已存在: "+out)
		}
		return err
	}
	_, werr := f.Write(buf.Bytes())
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

// Summary 给人读的简要文本：每个验收一行，再列缺口数量；完整字段见 --json。
func Summary(m map[string]any) string {
	var out strings.Builder
	out.WriteString("追溯结论：" + text(m["status"]) + "（只读汇总，不代表批准或完成）\n")
	rows, _ := m["matrix"].([]any)
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		slices, tests := list(row["slices"]), list(row["verifications"])
		mark := "✓"
		if len(slices) == 0 || len(tests) == 0 {
			mark = "缺口"
		}
		out.WriteString("  " + mark + "  " + text(row["acceptance"]) + "  切片 " + orNone(slices) + "；验证 " + orNone(tests) + "；提交 " + strconv.Itoa(len(list(row["commits"]))) + "\n")
	}
	gaps, _ := m["gaps"].(map[string]any)
	for _, k := range []struct{ key, label string }{{"acceptance_without_slice", "无切片的验收"}, {"acceptance_without_verification", "无验证的验收"}, {"commits_without_ticket", "无 Ticket 的提交"}, {"commits_unmapped_ticket", "Ticket 未对应切片的提交"}, {"basis_changed", "依据摘要已变化"}} {
		if v, _ := gaps[k.key].([]any); len(v) > 0 {
			out.WriteString(k.label + "：" + strconv.Itoa(len(v)) + " 项\n")
		}
	}
	out.WriteString("完整字段：--json；矩阵页：--html <项目外新文件>")
	return out.String()
}

func text(v any) string { s, _ := v.(string); return s }

func list(v any) []string {
	out := []string{}
	items, _ := v.([]any)
	for _, item := range items {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func orNone(v []string) string {
	if len(v) == 0 {
		return "无"
	}
	return strings.Join(v, ", ")
}
