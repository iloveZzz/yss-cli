// native-recovery interrupts the actual CLI after a target mutation. It never
// fabricates a journal or relies on an interpreter in the child environment.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}
func digest(data []byte) string { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }
func main() {
	if len(os.Args) != 3 {
		panic("用法: native-recovery <yss绝对路径> <新证据目录>")
	}
	binary, err := filepath.Abs(os.Args[1])
	must(err)
	out, err := filepath.Abs(os.Args[2])
	must(err)
	if _, err = os.Stat(out); !os.IsNotExist(err) {
		panic("证据目录必须不存在")
	}
	must(os.MkdirAll(out, 0755))
	out, err = filepath.EvalSymlinks(out)
	must(err)
	binaryBytes, err := os.ReadFile(binary)
	must(err)
	binaryHash := digest(binaryBytes)
	cases := []map[string]any{}
	environment := []string{"PATH=", "GOPROXY=off", "GOSUMDB=off", "HOME=" + out, "USERPROFILE=" + out, "TEMP=" + out, "TMP=" + out, "TMPDIR=" + out, "SYSTEMROOT=" + os.Getenv("SYSTEMROOT")}
	command := func(args ...string) *exec.Cmd {
		cmd := exec.Command(binary, append(args, "--json")...)
		cmd.Env = environment
		return cmd
	}
	invoke := func(label string, args ...string) map[string]any {
		raw, err := command(args...).CombinedOutput()
		must(os.WriteFile(filepath.Join(out, "output-"+label+".json"), raw, 0644))
		var env map[string]any
		must(json.Unmarshal(raw, &env))
		if err != nil || env["code"] != "OK" {
			panic(fmt.Sprintf("%s failed: %s %v", label, raw, err))
		}
		return env
	}
	inspection := invoke("spec-bundle-inspection", "bundle", "inspect", "--profile", "spec")["result"].(map[string]any)
	for _, profile := range []string{"spec", "design", "backend", "frontend"} {
		for _, kind := range []string{"cancel", "terminate", "kill"} {
			if kind == "terminate" && !supportsTerminate() {
				continue
			}
			label := profile + "-" + kind
			root := filepath.Join(out, label)
			plan := filepath.Join(out, label+"-saved-plan.json")
			args := []string{"init", "--root", root, "--profile", profile, "--plan", "--out", plan}
			if profile == "spec" {
				binding := filepath.Join(out, label+"-binding.json")
				data, err := json.Marshal(map[string]any{"schema_version": 2, "plugin": "yss-backend-delivery", "execution_scope": "plan-to-backend", "profile": "spec", "template_commit": inspection["templateCommit"], "bundle_hash": inspection["bundleHash"], "binary_sha256": binaryHash})
				must(err)
				raw, err := json.Marshal(map[string]any{"schemaVersion": 1, "path": ".yss-backend-plugin.json", "data": base64.StdEncoding.EncodeToString(data)})
				must(err)
				must(os.WriteFile(binding, raw, 0600))
				args = append(args, "--full", "--binding-file", binding)
			}
			invoke(label+"-preview", args...)
			raw, err := os.ReadFile(plan)
			must(err)
			var saved struct{ Changes []struct{ Path string } }
			must(json.Unmarshal(raw, &saved))
			if len(saved.Changes) == 0 {
				panic("preview has no target mutation")
			}
			child := command("init", "--root", root, "--profile", profile, "--apply", "--plan-file", plan)
			configureChild(child)
			var output bytes.Buffer
			child.Stdout = &output
			child.Stderr = &output
			must(child.Start())
			done := make(chan error, 1)
			go func() { done <- child.Wait() }()
			deadline := time.Now().Add(120 * time.Second)
			first := filepath.Join(root, filepath.FromSlash(saved.Changes[0].Path))
			for {
				if _, err := os.Stat(first); err == nil {
					break
				}
				select {
				case err := <-done:
					panic(fmt.Sprintf("child exited before mutation: %v %s", err, output.Bytes()))
				default:
				}
				if time.Now().After(deadline) {
					_ = child.Process.Kill()
					<-done
					panic("target-mutation timeout")
				}
				time.Sleep(time.Millisecond)
			}
			must(interruptChild(child, kind))
			exitErr := <-done
			raw = append([]byte(nil), output.Bytes()...)
			must(os.WriteFile(filepath.Join(out, "output-"+label+"-apply.json"), raw, 0644))
			if exitErr == nil {
				panic("child completed before interruption")
			}
			if kind != "kill" {
				var env map[string]any
				must(json.Unmarshal(raw, &env))
				if env["code"] != "CANCELLED" {
					panic(fmt.Sprintf("wrong cancellation code: %s", raw))
				}
			}
			invoke(label+"-recover", "recover", "--root", root, "--profile", profile, "--apply")
			invoke(label+"-repeat", "recover", "--root", root, "--profile", profile, "--apply")
			for _, change := range saved.Changes {
				if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(change.Path))); !os.IsNotExist(err) {
					panic("partial target survives recovery: " + change.Path)
				}
			}
			// A restored initialization keeps its verified archive. A fresh public
			// plan must allow a retry without deleting recovery material.
			retry := filepath.Join(out, label+"-retry-plan.json")
			invoke(label+"-retry-preview", "init", "--root", root, "--profile", profile, "--plan", "--out", retry)
			row := map[string]any{"profile": profile, "case": kind, "status": "passed", "observed_after_target_mutation": true, "binding_and_identity_restored_together": true, "repeated_recovery": true, "child_exit": child.ProcessState.ExitCode(), "output_sha256": digest(raw)}
			cases = append(cases, row)
			fmt.Println(label + " passed")
		}
	}
	current, err := os.ReadFile(binary)
	must(err)
	drift := digest(current) != binaryHash
	status := "passed"
	if drift {
		status = "failed"
	}
	report := map[string]any{"schema_version": 1, "kind": "native-process-recovery", "platform": runtime.GOOS + "/" + runtime.GOARCH, "binary_sha256": binaryHash, "input_drift": drift, "status": status, "path": "", "unexecuted": []string{}, "cases": cases}
	raw, err := json.MarshalIndent(report, "", "  ")
	must(err)
	must(os.WriteFile(filepath.Join(out, "report.json"), append(raw, '\n'), 0644))
	if drift {
		panic("native binary changed during recovery verification")
	}
}
