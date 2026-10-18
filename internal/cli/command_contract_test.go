package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func captureCommand(t *testing.T, ctx context.Context, args []string, tty bool) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := RunWithTerminal(ctx, args, &out, &errOut, func(io.Writer) bool { return tty })
	return code, out.String(), errOut.String()
}

func commandEnvelope(t *testing.T, out string) map[string]any {
	t.Helper()
	var env map[string]any
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("invalid envelope: %v\n%s", err, out)
	}
	return env
}

// Catches changes to flag scope, strict booleans and command-specific duplicates.
func TestCLIContractFlags(t *testing.T) {
	ctx := context.Background()
	for _, args := range [][]string{
		{"--json", "version"}, {"version", "--json"},
		{"version", "--json=false", "--json=true"},
		{"version", "--human=false", "--json"},
	} {
		code, out, errOut := captureCommand(t, ctx, args, false)
		if code != 0 || errOut != "" || commandEnvelope(t, out)["status"] != "ok" {
			t.Fatalf("%v: %d %s %s", args, code, out, errOut)
		}
	}
	missing := filepath.Join(t.TempDir(), "absent")
	for _, args := range [][]string{
		{"version", "--json=1"}, {"version", "--json=TRUE"},
		{"version", "-v"}, {"version", "-hV"}, {"upgrade", "--root", missing},
		{"check", "--root"},
		{"lifecycle", "route", "--root", missing, "--task", "a", "--task", "b"},
		{"scaffold", "ddd", "--project-name", "a", "--project-name", "b"},
		{"upgrade", "--to", "2.0.0", "--to", "2.1.0"},
	} {
		code, out, errOut := captureCommand(t, ctx, append(args, "--json"), false)
		if code != 2 || errOut != "" || commandEnvelope(t, out)["code"] != "ARGUMENT" {
			t.Fatalf("%v: %d %s %s", args, code, out, errOut)
		}
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("argument errors touched project root: %v", err)
	}
}

func TestCLIContractHelp(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent")
	for _, args := range [][]string{
		{"lifecycle", "route", "--root", missing, "--task", "--help", "--apply"},
		{"scaffold", "ddd", "--help", "--force"},
	} {
		code, out, errOut := captureCommand(t, context.Background(), args, false)
		if code != 0 || errOut != "" || !strings.Contains(out, "前置条件") || !strings.Contains(out, "常见错误") {
			t.Fatalf("help validated execution flags: %v: %d %s %s", args, code, out, errOut)
		}
	}
	code, out, errOut := captureCommand(t, context.Background(), []string{"help", "unknown-command"}, false)
	if code != 2 || out != "" || !strings.Contains(errOut, "ARGUMENT") {
		t.Fatalf("unknown help: %d %s %s", code, out, errOut)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("help created project root")
	}
}

func TestCLIContractOutput(t *testing.T) {
	ctx := context.Background()
	code, out, errOut := captureCommand(t, ctx, []string{"version"}, true)
	if code != 0 || errOut != "" || !strings.Contains(out, "CLI 版本：") || !strings.Contains(out, "协议版本：") {
		t.Fatalf("TTY: %d %s %s", code, out, errOut)
	}
	code, out, errOut = captureCommand(t, ctx, []string{"version"}, false)
	plain := commandEnvelope(t, out)
	if code != 0 || errOut != "" || plain["version"] == nil || plain["outputVersion"] != nil {
		t.Fatalf("pipe: %d %s %s", code, out, errOut)
	}
	for _, bad := range []bool{false, true} {
		for _, diagnostics := range []bool{false, true} {
			args := []string{"version"}
			if bad {
				args = append(args, "--bogus")
			}
			args = append(args, "--json")
			if diagnostics {
				args = append(args, "--diagnostics")
			}
			code, out, errOut := captureCommand(t, ctx, args, false)
			env := commandEnvelope(t, out)
			wantCode, wantStatus, wantExit := "OK", "ok", 0
			if bad {
				wantCode, wantStatus, wantExit = "ARGUMENT", "error", 2
			}
			if code != wantExit || errOut != "" || env["outputVersion"] != float64(1) || env["protocolVersion"] != float64(1) || env["command"] != "version" || env["code"] != wantCode || env["status"] != wantStatus {
				t.Fatalf("envelope: %v: %d %s %s", args, code, out, errOut)
			}
			if _, ok := env["result"].(map[string]any); !ok {
				t.Fatalf("missing result: %s", out)
			}
			_, hasDiagnostic := env["diagnostic"]
			if hasDiagnostic != (bad && diagnostics) {
				t.Fatalf("diagnostic presence: %s", out)
			}
		}
	}
}
