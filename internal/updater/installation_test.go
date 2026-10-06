package updater

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/domain"
)

func TestStatusDiagnosesDriftWithoutChangingInstallation(t *testing.T) {
	archive, sha := fixture(t, nil, runtime.GOOS+"/"+runtime.GOARCH)
	tool := filepath.Join(root(t), "tools")
	p, err := Build(tool, archive, sha)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Apply(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	changed := []byte("replacement binary")
	if err = os.WriteFile(filepath.Join(tool, fileName()), changed, 0755); err != nil {
		t.Fatal(err)
	}
	out, err := Status(tool)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(out)
	var report struct {
		InstallationConsistent bool
		Diagnostic             struct {
			ToolRoot                  string
			RunningProgramMatchesRoot bool
			Files                     []struct {
				Path, Status     string
				Expected, Actual struct{ Digest string }
			}
			NextSteps []string
		}
	}
	if err = json.Unmarshal(b, &report); err != nil {
		t.Fatal(err)
	}
	if report.InstallationConsistent || report.Diagnostic.ToolRoot != tool || report.Diagnostic.RunningProgramMatchesRoot {
		t.Fatalf("wrong status: %s", b)
	}
	found := false
	for _, f := range report.Diagnostic.Files {
		if f.Path == fileName() && f.Status == "changed" && f.Expected.Digest != f.Actual.Digest {
			found = true
		}
	}
	if !found || len(report.Diagnostic.NextSteps) == 0 {
		t.Fatalf("missing drift diagnosis: %s", b)
	}
	after, _ := os.ReadFile(filepath.Join(tool, fileName()))
	if string(after) != string(changed) {
		t.Fatal("status rewrote binary")
	}
	_, err = Build(tool, archive, sha)
	if updateErrorCode(err) != "CONFLICT" || !strings.Contains(err.Error(), "受管程序文件与安装清单不一致") || !strings.Contains(err.Error(), tool) {
		t.Fatalf("unhelpful conflict: %v", err)
	}
}

type noNetworkTransport struct{ calls int }

func (n *noNetworkTransport) RoundTrip(*http.Request) (*http.Response, error) {
	n.calls++
	return nil, fmt.Errorf("unexpected network")
}

func TestUpgradeExplainsOldReceiptAndRunningVersionBeforeNetworking(t *testing.T) {
	archive, sha := fixture(t, nil, runtime.GOOS+"/"+runtime.GOARCH)
	tool := filepath.Join(root(t), "tools")
	p, err := Build(tool, archive, sha)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Apply(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{receiptRef, "release-manifest.json"} {
		b, _ := os.ReadFile(filepath.Join(tool, name))
		var v map[string]any
		if err = json.Unmarshal(b, &v); err != nil {
			t.Fatal(err)
		}
		v["cliVersion"] = "1.0.1-dev.1"
		if err = os.WriteFile(filepath.Join(tool, name), encode(v), 0644); err != nil {
			t.Fatal(err)
		}
	}
	transport := &noNetworkTransport{}
	client := UpgradeClient{HTTPClient: &http.Client{Transport: transport}, Executable: func() (string, error) { return filepath.Join(tool, fileName()), nil }}
	for _, drift := range []bool{false, true} {
		if drift {
			if err = os.WriteFile(filepath.Join(tool, fileName()), []byte("new binary"), 0755); err != nil {
				t.Fatal(err)
			}
		}
		_, err = client.Run(context.Background(), UpgradeRequest{})
		want := "INSTALLATION"
		if drift {
			want = "CONFLICT"
		}
		if updateErrorCode(err) != want {
			t.Fatalf("drift %v: %v", drift, err)
		}
		var report interface{ ErrorResult() any }
		if !errors.As(err, &report) {
			t.Fatal("no structured diagnostic")
		}
		b, _ := json.Marshal(report.ErrorResult())
		if !strings.Contains(string(b), `"recordedVersion":"1.0.1-dev.1"`) || !strings.Contains(string(b), `"runningVersion":"`+domain.Version+`"`) {
			t.Fatalf("missing version facts: %s", b)
		}
		if transport.calls != 0 {
			t.Fatal("conflicting installation accessed network")
		}
	}
}

func TestStatusReportsMissingFilesAndPermissionsAndRemainsReadOnly(t *testing.T) {
	for _, change := range []string{"missing", "mode"} {
		t.Run(change, func(t *testing.T) {
			archive, sha := fixture(t, nil, runtime.GOOS+"/"+runtime.GOARCH)
			tool := filepath.Join(root(t), "tools")
			p, err := Build(tool, archive, sha)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = Apply(context.Background(), p); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(tool, "README.md")
			if change == "missing" {
				err = os.Remove(file)
			} else {
				if runtime.GOOS == "windows" {
					t.Skip("Windows normalizes executable permission bits")
				}
				err = os.Chmod(file, 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			out, err := Status(tool)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := json.Marshal(out)
			if !strings.Contains(string(b), `"installationConsistent":false`) || !strings.Contains(string(b), `"path":"README.md","status":"changed"`) {
				t.Fatalf("missing diagnostic: %s", b)
			}
			if change == "missing" {
				if _, err = os.Stat(file); !os.IsNotExist(err) {
					t.Fatal("status recreated file")
				}
			} else {
				info, _ := os.Stat(file)
				if info.Mode().Perm() != 0600 {
					t.Fatal("status changed permission")
				}
			}
		})
	}
}

func TestBareToolDirectoryConflictIsDiagnosedBeforeNetworking(t *testing.T) {
	tool := root(t)
	if err := os.WriteFile(filepath.Join(tool, "README.md"), []byte("user content"), 0644); err != nil {
		t.Fatal(err)
	}
	transport := &noNetworkTransport{}
	client := UpgradeClient{HTTPClient: &http.Client{Transport: transport}}
	_, err := client.Run(context.Background(), UpgradeRequest{ToolRoot: tool})
	if updateErrorCode(err) != "CONFLICT" || transport.calls != 0 {
		t.Fatalf("bare collision: %v, requests %d", err, transport.calls)
	}
}

func TestStatusPrefersRecoveryWhenInterruptedUpgradeHasMixedFiles(t *testing.T) {
	tool := filepath.Join(root(t), "tools")
	installProgram(t, tool, nil)
	interruptProgram(t, tool)
	out, err := Status(tool)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(out)
	var status struct {
		RecoveryRequired bool
		Diagnostic       struct {
			Status    string
			Files     []struct{ Status string }
			NextSteps []string
		}
	}
	if err = json.Unmarshal(b, &status); err != nil {
		t.Fatal(err)
	}
	if !status.RecoveryRequired || status.Diagnostic.Status != "incomplete" || !strings.Contains(strings.Join(status.Diagnostic.NextSteps, "\n"), "update recover") || strings.Contains(strings.Join(status.Diagnostic.NextSteps, "\n"), "upgrade --tool-root") {
		t.Fatalf("wrong recovery guidance: %s", b)
	}
	found := false
	for _, f := range status.Diagnostic.Files {
		if f.Status == "changed" {
			found = true
		}
	}
	if !found {
		t.Fatal("lost mixed file facts")
	}
	if _, err = Recover(context.Background(), tool); err != nil {
		t.Fatal(err)
	}
}
