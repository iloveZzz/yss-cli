package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHelpExplainsTheSelectedCommandWithoutCreatingAProject(t *testing.T) {
	root := filepath.Join(t.TempDir(), "absent-project")
	for _, args := range [][]string{
		{"init", "-h", "--root", root},
		{"init", "--help", "--root", root},
		{"help", "init", "--root", root},
		{"--help", "init", "--root", root},
	} {
		var out, stderr bytes.Buffer
		if code := Run(context.Background(), args, &out, &stderr); code != 0 {
			t.Fatalf("help %v: exit %d: %s", args, code, stderr.String())
		}
		for _, want := range []string{"用法: yss init", "--project-name", "示例", "--profile spec"} {
			if !strings.Contains(out.String(), want) {
				t.Fatalf("help %v missing %q: %s", args, want, out.String())
			}
		}
		if _, err := os.Stat(root); !os.IsNotExist(err) {
			t.Fatal("help accessed or created a project")
		}
	}
}

func TestHelpProvidesNestedUsageTutorialAndRejectsUnknownCommands(t *testing.T) {
	for _, args := range [][]string{{}, {"-h"}, {"--help"}, {"help"}} {
		var out, stderr bytes.Buffer
		if code := Run(context.Background(), args, &out, &stderr); code != 0 {
			t.Fatalf("root help: %d %s", code, stderr.String())
		}
		for _, want := range []string{"--profile spec", "--profile design", "--profile backend", "--profile frontend", "yss upgrade", "yss help tutorial"} {
			if !strings.Contains(out.String(), want) {
				t.Fatalf("root help missing %q", want)
			}
		}
	}
	for _, row := range []struct {
		args []string
		want string
	}{
		{[]string{"update", "apply", "-h"}, "--plan-file"},
		{[]string{"help", "lifecycle", "route"}, "--base"},
		{[]string{"bundle", "export", "--help"}, "--out"},
		{[]string{"help", "tutorial", "--json"}, "1.0.0"},
		{[]string{"runtime", "begin", "-h"}, "UNPORTED"},
		{[]string{"project-ci", "install", "-h"}, "--scope native-go"},
		{[]string{"xml", "query", "-h"}, "pom.xml"},
		{[]string{"help", "compat", "create-yss-spec"}, "--native"},
		{[]string{"help", "compat-api", "native.snapshot"}, "stdin"},
	} {
		var out, stderr bytes.Buffer
		if code := Run(context.Background(), row.args, &out, &stderr); code != 0 || !strings.Contains(out.String(), row.want) {
			t.Fatalf("help %v: %d %s %s", row.args, code, out.String(), stderr.String())
		}
	}
	for _, args := range [][]string{{"nonexistent", "-h"}, {"update", "nonexistent", "--help"}, {"help", "lifecycle", "nonexistent"}, {"compat", "nonexistent", "--help"}, {"compat-api", "nonexistent", "--help"}} {
		var out, stderr bytes.Buffer
		if code := Run(context.Background(), args, &out, &stderr); code != 2 || !strings.Contains(stderr.String(), "ARGUMENT") {
			t.Fatalf("unknown help %v: %d %s %s", args, code, out.String(), stderr.String())
		}
	}
}

func TestDailyHelpDoesNotAdvertiseUnsupportedConsumerFlags(t *testing.T) {
	for _, action := range []string{"route", "verify-daily"} {
		var out, stderr bytes.Buffer
		if code := Run(context.Background(), []string{"lifecycle", action, "--help"}, &out, &stderr); code != 0 {
			t.Fatal(stderr.String())
		}
		for _, flag := range []string{"--home", "--run-dir", "--tool-root"} {
			if strings.Contains(out.String(), flag) {
				t.Fatalf("daily %s help advertises unsupported %s", action, flag)
			}
		}
	}
}
