package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestUnknownInputExplainsCorrectionBeforeAccessingProject(t *testing.T) {
	root := filepath.Join(t.TempDir(), "absent")
	for _, row := range []struct {
		args []string
		want string
	}{
		{[]string{"upadate"}, "update"},
		{[]string{"update", "staus"}, "status"},
		{[]string{"init", "--profiel", "spec"}, "--profile"},
		{[]string{"init", "--profile", "wat"}, "spec|design|backend|frontend"},
		{[]string{"init", "--profile"}, "<Profile>"},
		{[]string{"init", "-x"}, "-x"},
		{[]string{"runtime", "complete", "--status", "nonsense", "--exit-code", "0"}, "--status"},
		{[]string{"evidence", "verify", "--kind", "nonsense"}, "--kind"},
		{[]string{"contract", "verify", "--kind", "scaffold", "--schema", "custom.json"}, "--schema"},
		{[]string{"evidence", "verify", "--kind", "verification", "--gate", "gate.test"}, "--gate"},
	} {
		var out, stderr bytes.Buffer
		args := append(append([]string{}, row.args...), "--json", "--root", root)
		if code := Run(context.Background(), args, &out, &stderr); code != 2 {
			t.Fatalf("%v: exit %d: %s %s", args, code, out.String(), stderr.String())
		}
		var env struct {
			Code   string
			Result struct{ Message string }
		}
		if err := json.Unmarshal(out.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		if env.Code != "ARGUMENT" || !strings.Contains(env.Result.Message, row.want) || !strings.Contains(env.Result.Message, "--help") {
			t.Fatalf("%v: %s", args, out.String())
		}
		if stderr.Len() != 0 {
			t.Fatalf("JSON error polluted stderr: %s", stderr.String())
		}
		if _, err := os.Stat(root); !os.IsNotExist(err) {
			t.Fatal("invalid input created a project")
		}
	}
}

type rejectHTTP struct{ calls *atomic.Int32 }

func (r rejectHTTP) RoundTrip(*http.Request) (*http.Response, error) {
	r.calls.Add(1)
	return nil, fmt.Errorf("unexpected network access")
}

func TestAllHelpIsOfflineAndProvidesAlignedParameters(t *testing.T) {
	var calls atomic.Int32
	previous := http.DefaultTransport
	http.DefaultTransport = rejectHTTP{&calls}
	t.Cleanup(func() { http.DefaultTransport = previous })
	root := filepath.Join(t.TempDir(), "absent")
	for key := range helpTopics {
		args := append([]string{"help"}, strings.Fields(key)...)
		args = append(args, "--root", root, "--json")
		var out, stderr bytes.Buffer
		if code := Run(context.Background(), args, &out, &stderr); code != 0 {
			t.Fatalf("%s: %d %s", key, code, stderr.String())
		}
		for _, want := range []string{"用法: yss", "参数:", "示例:", "-h, --help"} {
			if !strings.Contains(out.String(), want) {
				t.Fatalf("%s missing %s", key, want)
			}
		}
	}
	for _, args := range [][]string{{"upgrade", "--chek"}, {"upgrade", "wat"}, {"upgrade", "--root", root}, {"upgrade", "--to"}} {
		var out, stderr bytes.Buffer
		if code := Run(context.Background(), args, &out, &stderr); code != 2 || !strings.Contains(stderr.String(), "--help") || out.Len() != 0 {
			t.Fatalf("%v: %d %s %s", args, code, out.String(), stderr.String())
		}
	}
	if calls.Load() != 0 {
		t.Fatal("help or invalid arguments accessed network")
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("help created project")
	}
}

func TestVersionShortcutAndRootCommandDescriptions(t *testing.T) {
	for _, args := range [][]string{{"-V", "--json"}, {"--version", "--json"}} {
		var out, stderr bytes.Buffer
		if code := Run(context.Background(), args, &out, &stderr); code != 0 {
			t.Fatal(stderr.String())
		}
		var env struct{ Command, Status string }
		if err := json.Unmarshal(out.Bytes(), &env); err != nil || env.Command != "version" || env.Status != "ok" {
			t.Fatalf("version: %s %v", out.String(), err)
		}
	}
	var out, stderr bytes.Buffer
	Run(context.Background(), nil, &out, &stderr)
	for _, want := range []string{"-V, --version", "[项目]", "[程序]", "升级 CLI 程序", "安装、恢复或回退指定本地发行包", "将项目模板升级"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("root help missing %s", want)
		}
	}
}
