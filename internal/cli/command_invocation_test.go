package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

func TestInvokeOptionTerminator(t *testing.T) {
	raw, code := Invoke(context.Background(), []string{"version", "--", "--help"})
	if code != 2 || !json.Valid(raw) {
		t.Fatalf("Invoke lost JSON at terminator: %d %s", code, raw)
	}
	env := commandEnvelope(t, string(raw))
	if env["code"] != "ARGUMENT" || env["command"] != "version" {
		t.Fatalf("terminator changed command: %s", raw)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeCheckFile(t, root, "-example.yaml", "name: other\n")
	raw, code = Invoke(context.Background(), []string{"check", "--root", root, "--", "-example.yaml"})
	if code != 0 || !json.Valid(raw) || commandEnvelope(t, string(raw))["status"] != "ok" {
		t.Fatalf("Invoke dash file: %d %s", code, raw)
	}
}

func TestCLIInvocationIsolation(t *testing.T) {
	for _, args := range [][]string{
		{"version", "--json", "--diagnostics"},
		{"version", "--json=1"},
		{"lifecycle", "route", "--task=a", "--task=b", "--json"},
		{"version", "--human=false", "--json=false"},
	} {
		captureCommand(t, context.Background(), args, false)
		code, out, errOut := captureCommand(t, context.Background(), []string{"version"}, false)
		env := commandEnvelope(t, out)
		if code != 0 || errOut != "" || env["version"] == nil || env["outputVersion"] != nil {
			t.Fatalf("state leaked after %v: %d %s %s", args, code, out, errOut)
		}
		raw, exit := Invoke(context.Background(), []string{"version"})
		if exit != 0 || commandEnvelope(t, string(raw))["diagnostic"] != nil {
			t.Fatalf("Invoke state leaked: %d %s", exit, raw)
		}
	}
}

func TestCLICancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		args []string
		exit int
	}{
		{[]string{"version", "--json"}, 1},
		{[]string{"lifecycle", "route", "--root", t.TempDir(), "--json"}, 2},
	} {
		code, out, errOut := captureCommand(t, ctx, tc.args, false)
		if code != tc.exit || errOut != "" || commandEnvelope(t, out)["code"] != "CANCELLED" {
			t.Fatalf("cancellation: %d %s %s", code, out, errOut)
		}
	}
}

func TestCLIErrorResultAndReceipt(t *testing.T) {
	for _, receipt := range []bool{false, true} {
		inv := &invocation{o: options{args: []string{"check"}, values: map[string]string{}, json: true}, command: "check", executed: true}
		report := newCheckReport("yss-check", nil)
		report.add(checkItem{Status: "failed", Findings: []checkFinding{{Code: "SCHEMA", File: "a.yaml"}}})
		_, err := report.finish()
		if receipt {
			inv.result = transaction.Result{}
			err = domain.Fail("CONFLICT", "fixture conflict")
		}
		var out, errOut bytes.Buffer
		code := writeOutcome(context.Background(), &out, &errOut, func(io.Writer) bool { return false }, inv, err)
		result := commandEnvelope(t, out.String())["result"].(map[string]any)
		if code != 1 || errOut.Len() != 0 {
			t.Fatalf("failure output: %d %s %s", code, out.String(), errOut.String())
		}
		if receipt {
			r := result["receipt"].(map[string]any)
			if result["fileApplication"] != "not-applied" || result["verification"] != "not-run" || r["fileApplication"] != "not-applied" || r["verification"] != "not-run" {
				t.Fatalf("receipt lost: %s", out.String())
			}
		} else if result["kind"] != "yss-check" || !strings.Contains(out.String(), "a.yaml") {
			t.Fatalf("ErrorResult lost: %s", out.String())
		}
	}
}
