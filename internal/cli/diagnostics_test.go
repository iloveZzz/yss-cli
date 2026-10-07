package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/domain"
)

func TestDiagnosticCatalogCoversPublicFailureSites(t *testing.T) {
	known := map[string]bool{}
	for _, code := range errorCodes {
		if known[code] {
			t.Fatalf("公开错误索引重复登记: %s", code)
		}
		known[code] = true
	}
	missing := map[string]bool{}
	pattern := regexp.MustCompile(`^[A-Z][A-Z0-9_]+$`)
	record := func(expr ast.Expr) {
		if literal, ok := expr.(*ast.BasicLit); ok && literal.Kind == token.STRING {
			code, _ := strconv.Unquote(literal.Value)
			if pattern.MatchString(code) && code != "OK" && !known[code] {
				missing[code] = true
			}
		}
	}
	err := filepath.WalkDir("..", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			// The frozen Spec API registers codes in its rule table rather than
			// domain.Fail. Include those literals without changing that protocol.
			if filepath.ToSlash(path) == "../compat/api.go" {
				if literal, ok := node.(*ast.BasicLit); ok && literal.Kind == token.STRING {
					value, _ := strconv.Unquote(literal.Value)
					if value != "YSS_" && strings.HasPrefix(value, "YSS_") && pattern.MatchString(value) {
						record(literal)
					}
				}
			}
			if call, ok := node.(*ast.CallExpr); ok && len(call.Args) > 0 {
				name := ""
				if f, ok := call.Fun.(*ast.SelectorExpr); ok {
					name = f.Sel.Name
				}
				if f, ok := call.Fun.(*ast.Ident); ok {
					name = f.Name
				}
				if name == "Fail" || name == "Wrap" || name == "fail" || name == "unavailable" {
					record(call.Args[0])
				}
			}
			if literal, ok := node.(*ast.CompositeLit); ok {
				if f, ok := literal.Type.(*ast.Ident); ok && f.Name == "legacyError" && len(literal.Elts) > 0 {
					record(literal.Elts[0])
				}
				if f, ok := literal.Type.(*ast.SelectorExpr); ok && f.Sel.Name == "Error" {
					for i, expr := range literal.Elts {
						if field, ok := expr.(*ast.KeyValueExpr); ok {
							if key, ok := field.Key.(*ast.Ident); ok && key.Name == "Code" {
								record(field.Value)
							}
						} else if i == 0 {
							record(expr)
						}
					}
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !known["UNKNOWN_ALIAS"] {
		missing["UNKNOWN_ALIAS"] = true
	}
	if len(missing) != 0 {
		codes := []string{}
		for code := range missing {
			codes = append(codes, code)
		}
		sort.Strings(codes)
		t.Fatalf("公开错误缺少帮助条目: %v", codes)
	}
	for _, code := range errorCodes {
		content, err := renderErrorHelp(code)
		if err != nil || !strings.Contains(content, "原因") || !strings.Contains(content, "处理") || !strings.Contains(content, "复验") {
			t.Fatalf("%s: %v", code, err)
		}
	}
}

func TestHumanFailureAndHelpRemainIndependentOfProjectExecution(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	root = filepath.Join(root, "missing project")
	var out, stderr bytes.Buffer
	if exit := Run(context.Background(), []string{"doctor", "--root", root, "--human"}, &out, &stderr); exit != 1 || out.Len() != 0 {
		t.Fatalf("exit=%d stdout=%s stderr=%s", exit, &out, &stderr)
	}
	for _, term := range []string{"原因：", "处理（只读）", "复验：", "相关帮助：", "PROJECT_ROOT_NOT_FOUND", "'" + root + "'"} {
		if !strings.Contains(stderr.String(), term) {
			t.Fatalf("缺少 %s: %s", term, &stderr)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, args := range [][]string{{"init", "--root", "--help"}, {"--wat", "--help", "init"}, {"help", "tutorial", "daily", "--human", "--json", "--diagnostics"}} {
		out.Reset()
		stderr.Reset()
		if exit := Run(ctx, args, &out, &stderr); exit != 0 || json.Valid(out.Bytes()) || out.Len() == 0 {
			t.Fatalf("help %v: %d %s %s", args, exit, &out, &stderr)
		}
	}
}

func TestDiagnosticsKeepLegacyAndUserModificationRecoveryBoundaries(t *testing.T) {
	for _, scenario := range []struct{ code, id string }{{"LEGACY_INTERRUPTED", "LEGACY_EXECUTOR_PENDING"}, {"RECOVERY_FAILED", "RECOVERY_USER_MODIFICATION"}} {
		err := domain.Explain(domain.Fail(scenario.code, "原始错误"), scenario.id, "已确认原因", map[string]any{"path": "file with spaces", "transaction": "id"})
		d := diagnosticFor(scenario.code, err, options{args: []string{"rollback"}, values: map[string]string{"root": "/project with spaces"}}, "spec", nil)
		for _, step := range d.Steps {
			if step.Effect == "write" {
				t.Fatalf("%s 不应推荐直接原生写恢复", scenario.id)
			}
		}
		if d.OriginalCause != "原始错误" || d.Cause != "已确认原因" {
			t.Fatalf("原因丢失: %#v", d)
		}
	}
}
