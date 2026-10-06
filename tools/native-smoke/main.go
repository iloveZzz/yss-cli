// Native smoke runs the built program without interpreters on PATH.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

func main() {
	if len(os.Args) != 3 {
		panic("用法: native-smoke <yss绝对路径> <新证据目录>")
	}
	binary, e := filepath.Abs(os.Args[1])
	must(e)
	out, e := filepath.Abs(os.Args[2])
	must(e)
	if _, e = os.Stat(out); !os.IsNotExist(e) {
		panic("证据目录必须不存在")
	}
	must(os.MkdirAll(out, 0755))
	out, e = filepath.EvalSymlinks(out)
	must(e)
	initialBinary, e := os.ReadFile(binary)
	must(e)
	initialHash := sha256.Sum256(initialBinary)
	binaryHash := hex.EncodeToString(initialHash[:])
	results := []map[string]any{}
	run := func(profile string, args ...string) map[string]any {
		started := time.Now()
		c := exec.Command(binary, append(args, "--json")...)
		c.Env = []string{"PATH=", "GOPROXY=off", "GOSUMDB=off", "HOME=" + out, "USERPROFILE=" + out, "TEMP=" + out, "TMP=" + out, "TMPDIR=" + out, "SYSTEMROOT=" + os.Getenv("SYSTEMROOT")}
		b, e := c.CombinedOutput()
		exit := 0
		if e != nil {
			if v, ok := e.(*exec.ExitError); ok {
				exit = v.ExitCode()
			} else {
				panic(e)
			}
		}
		var v map[string]any
		must(json.Unmarshal(b, &v))
		log := fmt.Sprintf("%03d.json", len(results)+1)
		h := sha256.Sum256(b)
		r := map[string]any{"profile": profile, "command": args, "exitCode": exit, "durationMs": time.Since(started).Milliseconds(), "envelopeRef": log, "sha256": hex.EncodeToString(h[:]), "code": v["code"], "status": v["status"]}
		results = append(results, r)
		must(os.WriteFile(filepath.Join(out, log), b, 0644))
		if exit != 0 || v["status"] != "ok" {
			panic(fmt.Sprintf("native smoke failed %v: %s", args, b))
		}
		return v
	}
	run("", "version")
	run("", "capabilities")
	for _, profile := range []string{"spec", "design", "backend", "frontend"} {
		root := filepath.Join(out, "project-"+profile)
		run(profile, "init", "--profile", profile, "--root", root)
		run(profile, "context", "verify", "--root", root)
		run(profile, "diff", "--root", root)
		run(profile, "doctor", "--root", root)
		unit := "work-unit.entry-triage"
		if profile == "backend" || profile == "frontend" {
			unit = "work-unit.harness-entry"
		}
		run(profile, "lifecycle", "query", "--root", root, "--id", unit)
		plan := filepath.Join(out, "sync-"+profile+".json")
		run(profile, "sync", "--root", root, "--plan", "--out", plan)
		run(profile, "sync", "--root", root, "--apply", "--plan-file", plan)
		run(profile, "skills", "list", "--root", root)
		run(profile, "assets", "list", "--root", root)
		run(profile, "migrate", "status", "--root", root)
		runtimeHome := filepath.Join(out, "runtime-store")
		v := run(profile, "runtime", "begin", "--root", root, "--home", runtimeHome, "--kind", "native-smoke")
		record, ok := v["result"].(map[string]any)
		if !ok {
			panic("runtime begin missing result")
		}
		id, ok := record["id"].(string)
		if !ok {
			panic("runtime begin missing id")
		}
		token, ok := record["token"].(string)
		if !ok {
			panic("runtime begin missing owner token")
		}
		run(profile, "runtime", "event", "--root", root, "--home", runtimeHome, "--id", id, "--token", token, "--type", "native-smoke", "--value", `{"runtime":"native-go"}`)
		run(profile, "runtime", "complete", "--root", root, "--home", runtimeHome, "--id", id, "--token", token, "--status", "passed", "--exit-code", "0")
		run(profile, "runtime", "run", "--root", root, "--home", runtimeHome, "--id", id)
		run(profile, "runtime", "events", "--root", root, "--home", runtimeHome, "--id", id)
		run(profile, "runtime", "commands", "--root", root, "--home", runtimeHome, "--id", id)
		run(profile, "runtime", "pin", "--root", root, "--home", runtimeHome, "--id", id, "--token", token, "--reason", "native-smoke-observation")
		run(profile, "runtime", "pins", "--root", root, "--home", runtimeHome, "--id", id)
		run(profile, "runtime", "unpin", "--root", root, "--home", runtimeHome, "--id", id, "--token", token, "--reason", "native-smoke-observation")
		run(profile, "runtime", "inspect", "--root", root, "--home", runtimeHome)
	}
	finalBinary, e := os.ReadFile(binary)
	must(e)
	finalHash := sha256.Sum256(finalBinary)
	drift := initialHash != finalHash
	status := "passed"
	if drift {
		status = "failed"
	}
	report := map[string]any{"schemaVersion": 1, "status": status, "binary_sha256": binaryHash, "input_drift": drift, "unexecuted": []string{}, "platform": runtime.GOOS + "/" + runtime.GOARCH, "path": "", "networkDependency": "none: built CLI has no network loader; GOPROXY/GOSUMDB off", "covered": "ported command surface only", "stableReady": false, "results": results}
	b, e := json.MarshalIndent(report, "", "  ")
	must(e)
	must(os.WriteFile(filepath.Join(out, "report.json"), append(b, '\n'), 0644))
	if drift {
		panic("native binary changed during verification")
	}
	fmt.Println(filepath.Join(out, "report.json"))
}
func must(e error) {
	if e != nil {
		panic(e)
	}
}
