package project

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
)

type Resolution struct {
	Path              string             `json:"path"`
	Choice            string             `json:"choice"`
	Before            domain.Descriptor  `json:"before"`
	Target            domain.Descriptor  `json:"target"`
	RuleID            string             `json:"ruleId,omitempty"`
	TargetPath        string             `json:"targetPath,omitempty"`
	DestinationBefore *domain.Descriptor `json:"destinationBefore,omitempty"`
	CandidateFile     string             `json:"candidateFile,omitempty"`
	CandidateDigest   string             `json:"candidateDigest,omitempty"`
	CandidateData     string             `json:"candidateData,omitempty"`
}
type ResolutionFile struct {
	SchemaVersion int          `json:"schemaVersion"`
	PlanDigest    string       `json:"planDigest"`
	Decisions     []Resolution `json:"decisions"`
}

func readRegular(file string, limit int64) ([]byte, error) {
	abs, e := filepath.Abs(file)
	if e != nil {
		return nil, e
	}
	canonical, e := filepath.EvalSymlinks(abs)
	if e != nil {
		return nil, e
	}
	if canonical != abs {
		return nil, domain.Fail("PATH", "材料不接受链接路径: "+file)
	}
	info, e := os.Lstat(abs)
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, domain.Fail("PATH", "材料必须是大小受限的普通文件")
	}
	return os.ReadFile(abs)
}
func ReadResolutionFile(file string) (ResolutionFile, error) {
	var out ResolutionFile
	raw, e := readRegular(file, 8*1024*1024)
	if e != nil {
		return out, e
	}
	if _, e = schema.Parse(raw); e != nil {
		return out, e
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e = d.Decode(&out); e != nil {
		return out, domain.Wrap("RESOLUTION", e)
	}
	if out.SchemaVersion != 1 || !digestPattern.MatchString(out.PlanDigest) {
		return out, domain.Fail("RESOLUTION", "未知决议版本或计划摘要")
	}
	for i, r := range out.Decisions {
		if r.CandidateFile != "" {
			if !filepath.IsAbs(r.CandidateFile) {
				r.CandidateFile = filepath.Join(filepath.Dir(file), r.CandidateFile)
			}
			r.CandidateFile, e = filepath.Abs(r.CandidateFile)
			if e != nil {
				return out, e
			}
			out.Decisions[i] = r
		}
	}
	return out, nil
}
func validateResolution(a AssetResult, r Resolution) error {
	if a.Action != "conflict" || a.Before != r.Before || a.Target != r.Target || a.RuleID != r.RuleID || a.TargetPath != r.TargetPath {
		return domain.Fail("RESOLUTION_STALE", "决议描述与原冲突不匹配: "+r.Path)
	}
	if (a.DestinationBefore == nil) != (r.DestinationBefore == nil) || a.DestinationBefore != nil && *a.DestinationBefore != *r.DestinationBefore {
		return domain.Fail("RESOLUTION_STALE", "改名目标已改变: "+r.Path)
	}
	allowed := false
	for _, choice := range a.Options {
		allowed = allowed || choice == r.Choice
	}
	if !allowed {
		return domain.Fail("RESOLUTION_POLICY", "资产政策不接受此决议: "+r.Path)
	}
	if r.Choice != "use-merged" && (r.CandidateFile != "" || r.CandidateDigest != "" || r.CandidateData != "") {
		return domain.Fail("RESOLUTION", "非合并决定不得携带候选字节")
	}
	return nil
}
func resolutionBytes(r Resolution) ([]byte, error) {
	if !digestPattern.MatchString(r.CandidateDigest) {
		return nil, domain.Fail("RESOLUTION", "合并候选必须登记 SHA-256")
	}
	var raw []byte
	var e error
	if r.CandidateFile != "" {
		raw, e = readRegular(r.CandidateFile, 16*1024*1024)
		if e != nil {
			return nil, e
		}
		if r.CandidateData != "" && !bytes.Equal(raw, mustDecode(r.CandidateData)) {
			return nil, domain.Fail("CANDIDATE_DRIFT", "候选材料在规划后改变: "+r.Path)
		}
	} else {
		raw, e = base64.StdEncoding.DecodeString(r.CandidateData)
		if e != nil {
			return nil, e
		}
	}
	if safefs.Digest(raw) != r.CandidateDigest {
		return nil, domain.Fail("CANDIDATE_DRIFT", "合并候选摘要不匹配: "+r.Path)
	}
	if bytes.Contains(raw, []byte("<<<<<<<")) || bytes.Contains(raw, []byte("|||||||")) || bytes.Contains(raw, []byte(">>>>>>>")) {
		return nil, domain.Fail("RESOLUTION", "候选仍有合并冲突标记: "+r.Path)
	}
	return raw, nil
}

// git merge-file is a standalone byte merge, never a Git repository operation.
// Explicit labels make overlapping candidates deterministic across temp roots.
func mergeCandidate(local, base, target []byte) (*Candidate, error) {
	if len(local) > 16*1024*1024 || len(base) > 16*1024*1024 || len(target) > 16*1024*1024 || bytes.IndexByte(local, 0) >= 0 || bytes.IndexByte(base, 0) >= 0 || bytes.IndexByte(target, 0) >= 0 {
		return nil, nil
	}
	if _, e := exec.LookPath("git"); e != nil {
		return nil, nil
	}
	dir, e := os.MkdirTemp("", "yss-merge-")
	if e != nil {
		return nil, e
	}
	defer os.RemoveAll(dir)
	for name, data := range map[string][]byte{"local": local, "base": base, "template": target} {
		if e = os.WriteFile(filepath.Join(dir, name), data, 0600); e != nil {
			return nil, e
		}
	}
	c := exec.Command("git", "merge-file", "-p", "--diff3", "-L", "local", "-L", "base", "-L", "template", filepath.Join(dir, "local"), filepath.Join(dir, "base"), filepath.Join(dir, "template"))
	var stderr bytes.Buffer
	c.Stderr = &stderr
	raw, e := c.Output()
	clean := e == nil
	if e != nil {
		var exit *exec.ExitError
		if !errors.As(e, &exit) || exit.ExitCode() < 1 || exit.ExitCode() > 127 {
			return nil, domain.Fail("MERGE", "生成三方候选失败: "+stderr.String())
		}
	}
	return &Candidate{Data: base64.StdEncoding.EncodeToString(raw), Digest: safefs.Digest(raw), Clean: clean}, nil
}

// ExportReview only writes a new directory outside the project. Its decision
// file starts empty; the conflict inventory supplies exact binding fields.
func ExportReview(p *Plan, out string) error {
	abs, e := filepath.Abs(out)
	if e != nil {
		return e
	}
	parent, e := filepath.EvalSymlinks(filepath.Dir(abs))
	if e != nil {
		return e
	}
	abs = filepath.Join(parent, filepath.Base(abs))
	rel, e := filepath.Rel(p.Root, abs)
	if e != nil {
		return e
	}
	if rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return domain.Fail("PROTECTED", "审查材料必须导出到项目外新目录")
	}
	for _, part := range strings.Split(filepath.ToSlash(abs), "/") {
		if strings.EqualFold(part, ".git") {
			return domain.Fail("PROTECTED", "审查材料不得写入 Git 内部目录")
		}
	}
	if e = os.Mkdir(abs, 0700); e != nil {
		return e
	}
	success := false
	defer func() {
		if !success {
			os.RemoveAll(abs)
		}
	}()
	write := func(ref string, raw []byte) error {
		path, e := safefs.Path(abs, ref)
		if e != nil {
			return e
		}
		if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			return e
		}
		f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		_, e = f.Write(raw)
		if ce := f.Close(); e == nil {
			e = ce
		}
		return e
	}
	inventory := []map[string]any{}
	if p.WorkLayout != nil {
		for _, c := range p.Changes {
			if c.After.Type != "file" {
				continue
			}
			ref := c.Path
			for from, to := range p.WorkLayout.Mapping {
				if to == c.Path {
					ref = from
					break
				}
			}
			conflict := false
			for _, a := range p.Assets {
				conflict = conflict || a.Path == c.Path && a.Action == "conflict"
			}
			if !conflict {
				if e = write("candidates/"+c.Path, mustDecode(c.Data)); e != nil {
					return e
				}
			}
			if e = write("layout-diff/"+c.Path+".diff", []byte("--- "+ref+"\n+++ "+c.Path+"\n"+lineComparison(mustRead(p.Root, ref), mustDecode(c.Data)))); e != nil {
				return e
			}
		}
		raw, e := jsonBytes(p.WorkLayout)
		if e != nil {
			return e
		}
		if e = write("layout-mapping.json", raw); e != nil {
			return e
		}
	}
	for _, a := range p.Assets {
		if a.Action != "conflict" {
			continue
		}
		entry := map[string]any{"asset": a, "decision": Resolution{Path: a.Path, Before: a.Before, Target: a.Target, RuleID: a.RuleID, TargetPath: a.TargetPath, DestinationBefore: a.DestinationBefore}}
		if a.Before.Type == "file" {
			raw, e := os.ReadFile(filepath.Join(p.Root, a.Path))
			if e != nil {
				return e
			}
			if safefs.Digest(raw) != a.Before.Digest {
				return domain.Fail("INPUT_DRIFT", "导出审查时输入改变: "+a.Path)
			}
			if e = write("local/"+a.Path, raw); e != nil {
				return e
			}
		}
		if a.TargetData != "" {
			if e = write("template/"+a.Path, mustDecode(a.TargetData)); e != nil {
				return e
			}
		}
		if a.BaselineAvailable {
			if e = write("base/"+a.Path, mustDecode(a.BaselineData)); e != nil {
				return e
			}
		}
		if a.Candidate != nil {
			if e = write("candidates/"+a.Path, mustDecode(a.Candidate.Data)); e != nil {
				return e
			}
			entry["candidateFile"] = filepath.Join(abs, "candidates", a.Path)
			entry["candidateDigest"] = a.Candidate.Digest
		}
		local := mustRead(p.Root, a.Path)
		template := mustDecode(a.TargetData)
		diff := []byte("--- local/" + a.Path + "\n+++ template/" + a.Path + "\n" + lineComparison(local, template))
		if e = write("diff/"+a.Path+".diff", diff); e != nil {
			return e
		}
		inventory = append(inventory, entry)
	}
	for ref, value := range map[string]any{"plan.json": p, "conflicts.json": inventory, "decisions.json": ResolutionFile{SchemaVersion: 1, PlanDigest: p.Digest, Decisions: []Resolution{}}} {
		raw, e := jsonBytes(value)
		if e != nil {
			return e
		}
		if e = write(ref, raw); e != nil {
			return e
		}
	}
	readme := []byte("# YSS 受管资产审查\n\nplan.json 为原计划。conflicts.json 提供逐项描述和可选决定。\n将选定项填入 decisions.json 的 decisions，choice 使用资产允许的值。\nuse-merged 必须填写候选 candidateFile 与原始字节 SHA-256 candidateDigest。\n候选不会直接写入项目；clean=true 也需要显式决定。\n基线不可用时仅有双向对照；不要推测旧模板。\n修改材料后重新生成决议计划，再应用保存计划。\n")
	if e = write("README.md", readme); e != nil {
		return e
	}
	success = true
	return nil
}
func mustRead(root, ref string) []byte { raw, _ := os.ReadFile(filepath.Join(root, ref)); return raw }
func lineComparison(local, target []byte) string {
	var b strings.Builder
	// Full byte comparison avoids silently dropping lines when no base exists.
	for _, line := range strings.SplitAfter(string(local), "\n") {
		if line != "" {
			b.WriteString("-")
			b.WriteString(line)
			if !strings.HasSuffix(line, "\n") {
				b.WriteString("\n\\ No newline at end of file\n")
			}
		}
	}
	for _, line := range strings.SplitAfter(string(target), "\n") {
		if line != "" {
			b.WriteString("+")
			b.WriteString(line)
			if !strings.HasSuffix(line, "\n") {
				b.WriteString("\n\\ No newline at end of file\n")
			}
		}
	}
	return b.String()
}
