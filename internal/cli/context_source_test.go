package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/bundle"
)

func TestContextCLIReadOnlyTemplateSource(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b, err := bundle.Load("spec")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := b.Files["CONTEXT.md"].Render(map[string]string{"projectName": "synthetic-context", "businessDomain": "test-only", "teamSize": "2"})
	if err != nil {
		t.Fatal(err)
	}
	cliGovPut(t, root, "CONTEXT.md", raw)
	for _, identity := range []string{
		`{"schema_version":1,"repository_mode":"template-source"}`,
		`{"schema_version":99,"repository_mode":"template-source"}`,
		`{"schema_version":1,"repository_mode":"project-instance"}`,
	} {
		cliGovPut(t, root, "yss-project.yaml", []byte(identity))
		before, _ := json.Marshal(cliGovSnapshot(t, root))
		var out, stderr bytes.Buffer
		exit := Run(context.Background(), []string{"context", "check", "--root", root, "--json"}, &out, &stderr)
		if identity == `{"schema_version":1,"repository_mode":"template-source"}` {
			if exit != 0 {
				t.Fatalf("合法模板源被拒绝: %s", out.String())
			}
			var envelope map[string]any
			if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope["result"].(map[string]any)["read_only"] != true {
				t.Fatal(envelope)
			}
		} else if exit == 0 {
			t.Fatal("非法源身份或无 metadata 实例被接受")
		}
		after, _ := json.Marshal(cliGovSnapshot(t, root))
		if !bytes.Equal(before, after) {
			t.Fatal("只读校验改变文件")
		}
	}
	if err := os.Remove(filepath.Join(root, "yss-project.yaml")); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if Run(context.Background(), []string{"context", "check", "--root", root, "--json"}, &out, &stderr) == 0 {
		t.Fatal("缺失身份被接受")
	}
}
