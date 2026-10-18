package kernel

import (
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"path"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
)

type snapshotGitObject struct {
	kind string
	raw  []byte
}
type taskSourceSnapshot struct {
	root    string
	tasks   map[string]string
	objects map[string]snapshotGitObject
	files   map[string]domain.Descriptor
	proof   map[string]any
}

func decodeSnapshotObjects(s *SemanticSession, rows []any) (map[string]snapshotGitObject, error) {
	objects := map[string]snapshotGitObject{}
	total := 0
	if len(rows) == 0 || len(rows) > 20000 {
		return nil, s.Reject("TASK_SNAPSHOT", "Git对象数量无效")
	}
	for _, value := range rows {
		row := ApMap(value)
		kind, oid := Text(row["type"]), Text(row["oid"])
		raw, err := base64.StdEncoding.Strict().DecodeString(Text(row["data_base64"]))
		total += len(raw)
		if err != nil || !SemHas([]string{"commit", "tree", "blob"}, kind) || len(oid) != 40 || objects[oid].kind != "" || total > 128<<20 {
			return nil, s.Reject("TASK_SNAPSHOT", "Git对象格式、重复或大小无效")
		}
		sum := sha1.Sum(append([]byte(fmt.Sprintf("%s %d\x00", kind, len(raw))), raw...))
		if hex.EncodeToString(sum[:]) != oid {
			return nil, s.Reject("TASK_SNAPSHOT", "Git对象内容摘要冲突")
		}
		objects[oid] = snapshotGitObject{kind, raw}
	}
	return objects, nil
}

// Snapshot queries are reconstructed from verified Git objects, never executed on the receiver.
func (s *SemanticSession) snapshotGit(root string, args []string) ([]byte, error) {
	snap := s.taskSnapshot
	if snap == nil || root != snap.root {
		return nil, s.Reject("TASK_SNAPSHOT", "未绑定快照工程")
	}
	if ApEqual(args, []string{"rev-parse", "--show-toplevel"}) {
		return []byte(root + "\n"), nil
	}
	if len(args) == 2 && args[0] == "rev-parse" && strings.HasSuffix(args[1], "^{commit}") {
		oid := strings.TrimSuffix(args[1], "^{commit}")
		if snap.objects[oid].kind == "commit" {
			return []byte(oid + "\n"), nil
		}
	}
	if len(args) == 3 && args[0] == "cat-file" && args[1] == "blob" && snap.objects[args[2]].kind == "blob" {
		return snap.objects[args[2]].raw, nil
	}
	if len(args) == 4 && args[0] == "ls-tree" && args[1] == "-r" && args[2] == "-z" {
		commit := snap.objects[args[3]]
		if commit.kind != "commit" {
			return nil, s.Reject("TASK_SNAPSHOT", "缺少原始commit对象")
		}
		tree := strings.SplitN(string(commit.raw), "\n", 2)[0]
		if !strings.HasPrefix(tree, "tree ") || len(tree) != 45 {
			return nil, s.Reject("TASK_SNAPSHOT", "commit缺少原始tree")
		}
		return snapshotTreeRows(s, snap.objects, strings.TrimPrefix(tree, "tree "), "", 0, nil)
	}
	return nil, s.Reject("TASK_SNAPSHOT", "快照不支持该Git查询")
}

func snapshotTreeRows(s *SemanticSession, objects map[string]snapshotGitObject, oid, prefix string, depth int, visit func(string, string, string) error) ([]byte, error) {
	object := objects[oid]
	if object.kind != "tree" || depth > 100 {
		return nil, s.Reject("TASK_SNAPSHOT", "Git tree缺失或深度超限")
	}
	raw := object.raw
	out := []byte{}
	names := map[string]bool{}
	for len(raw) > 0 {
		space, nul := bytes.IndexByte(raw, ' '), bytes.IndexByte(raw, 0)
		if space < 1 || nul <= space || len(raw) < nul+21 {
			return nil, s.Reject("TASK_SNAPSHOT", "Git tree截断")
		}
		mode, name := string(raw[:space]), string(raw[space+1:nul])
		child := hex.EncodeToString(raw[nul+1 : nul+21])
		raw = raw[nul+21:]
		if !utf8.ValidString(name) || strings.Contains(name, "/") || name == "." || name == ".." || name == "" || names[name] || !SemHas([]string{"40000", "100644", "100755", "120000", "160000"}, mode) {
			return nil, s.Reject("TASK_SNAPSHOT", "Git tree路径或类型无效")
		}
		names[name] = true
		ref := path.Join(prefix, name)
		if !ContractPath(ref) {
			return nil, s.Reject("TASK_SNAPSHOT", "Git tree路径越界")
		}
		if mode == "40000" {
			rows, err := snapshotTreeRows(s, objects, child, ref, depth+1, visit)
			if err != nil {
				return nil, err
			}
			out = append(out, rows...)
		} else {
			if visit != nil {
				if err := visit(ref, mode, child); err != nil {
					return nil, err
				}
			}
			kind := "blob"
			if mode == "160000" {
				kind = "commit"
			}
			out = append(out, []byte(mode+" "+kind+" "+child+"\t"+ref+"\x00")...)
		}
		if len(out) > 64<<20 {
			return nil, s.Reject("TASK_SNAPSHOT", "Git tree展开超限")
		}
	}
	return out, nil
}

func (s *SemanticSession) IsTaskSnapshotRoot(root string) bool {
	return s.taskSnapshot != nil && root == s.taskSnapshot.root
}
func (s *SemanticSession) TaskPath(root, ref string) (string, error) {
	if s.IsTaskSnapshotRoot(root) {
		return safefs.Path(s.Root, s.LocalRef(ref))
	}
	return safefs.Path(root, ref)
}
func (s *SemanticSession) TaskDescriptor(root, ref string) (domain.Descriptor, error) {
	if s.IsTaskSnapshotRoot(root) {
		d, err := s.V.Watch(s.LocalRef(ref))
		if err != nil {
			return d, err
		}
		if d.Type == "file" {
			captured, ok := s.taskSnapshot.files[ref]
			if !ok || captured.Digest != d.Digest {
				return d, s.Reject("TASK_SNAPSHOT", "范围文件未被完整封存: "+ref)
			}
			return captured, nil
		}
		return d, nil
	}
	v := s.V
	if root != s.Root {
		v = s.ExternalViews[root]
		if v == nil {
			return domain.Descriptor{}, s.Reject("REPOSITORY", "工程未登记")
		}
	}
	return v.Watch(ref)
}
func (s *SemanticSession) ValidateSnapshotTask(ref string, task map[string]any) error {
	if s.taskSnapshot == nil {
		return nil
	}
	bound, err := s.Bind(ref)
	if err != nil {
		return err
	}
	if s.taskSnapshot.tasks[ref] != strings.TrimPrefix(bound.Digest, "sha256:") || ApMap(task["contract"])["kind"] != "lifecycle-work-unit" || task["workflow_status"] != "resolved" {
		return s.Reject("TASK_SNAPSHOT", "任务未封存为已完成的来源事实")
	}
	return nil
}

// Only the manifest-validated immutable source view can install this bounded replay context.
func (s *SemanticSession) BindTaskSnapshot(proof map[string]any) error {
	if s.RuleSession == nil || s.GovernanceVersion != 2 || ApNumber(proof["schema_version"]) != 1 || s.taskSnapshot != nil {
		return s.Reject("TASK_SNAPSHOT", "快照来源或版本无效")
	}
	root := Text(proof["repository_root"])
	if !snapshotOriginRoot(root) {
		return s.Reject("TASK_SNAPSHOT", "来源根无效")
	}
	objects, err := decodeSnapshotObjects(s, ApArray(proof["objects"]))
	if err != nil {
		return err
	}
	snap := &taskSourceSnapshot{root: root, objects: objects, tasks: map[string]string{}, files: map[string]domain.Descriptor{}, proof: proof}
	for _, asset := range ApRows(proof["tasks"]) {
		ref := Text(asset["ref"])
		raw, err := s.EvidenceAsset(asset)
		if err != nil {
			return err
		}
		task, err := s.Doc(ref)
		if err != nil {
			return err
		}
		if snap.tasks[ref] != "" || ApNumber(task["schema_version"]) != 3 || task["workflow_status"] != "resolved" || ApMap(task["contract"])["kind"] != "lifecycle-work-unit" || ApMap(task["baseline"])["repository_root"] != root {
			return s.Reject("TASK_SNAPSHOT", "来源任务身份或工程冲突")
		}
		snap.tasks[ref] = safefs.Digest(raw)
	}
	if len(snap.tasks) == 0 {
		return s.Reject("TASK_SNAPSHOT", "缺少已完成来源任务")
	}
	for _, asset := range ApRows(proof["files"]) {
		ref := Text(asset["ref"])
		raw, err := s.EvidenceAsset(map[string]any{"ref": ref, "digest": asset["digest"]})
		if err != nil {
			return err
		}
		mode := ApNumber(asset["mode"])
		if snap.files[ref].Type != "" || mode < 0 || mode > 0777 {
			return s.Reject("TASK_SNAPSHOT", "来源文件权限或重复无效")
		}
		snap.files[ref] = domain.Descriptor{Type: "file", Digest: safefs.Digest(raw), Mode: uint32(mode)}
	}
	s.taskSnapshot = snap
	return nil
}

func (s *SemanticSession) CaptureTaskSnapshot(refs []string) (map[string]any, []string, error) {
	if s.taskSnapshot != nil {
		all := map[string]bool{}
		for _, ref := range refs {
			all[ref] = true
		}
		referenced := map[string]bool{}
		for _, ref := range refs {
			doc, err := snapshotTaskDocument(s, ref)
			if err != nil {
				return nil, nil, err
			}
			if ApNumber(doc["schema_version"]) == 3 && ApMap(doc["contract"])["kind"] == "lifecycle-work-unit" && doc["workflow_status"] == "resolved" {
				referenced[ref] = true
			}
		}
		if len(referenced) != len(s.taskSnapshot.tasks) {
			return nil, nil, s.Reject("TASK_SNAPSHOT", "任务快照必须等于实际引用的已完成来源任务")
		}
		for ref := range s.taskSnapshot.tasks {
			if !referenced[ref] {
				return nil, nil, s.Reject("TASK_SNAPSHOT", "快照含未引用来源任务")
			}
		}
		for ref := range s.taskSnapshot.tasks {
			task, err := s.Doc(ref)
			if err != nil {
				return nil, nil, err
			}
			for _, scope := range ApStrings(task["allowed_write_paths"]) {
				files, err := s.Scan(scope)
				if err != nil {
					return nil, nil, err
				}
				for _, file := range files {
					all[file] = true
				}
			}
			for _, bucket := range []any{task["expected_outputs"], task["expected_evidence_files"], ApMap(task["result"])["evidence_refs"]} {
				for _, file := range ApStrings(bucket) {
					all[file] = true
				}
			}
		}
		if len(all) != len(s.taskSnapshot.files) {
			return nil, nil, s.Reject("TASK_SNAPSHOT", "封存文件必须等于来源与实际任务范围闭包")
		}
		out := []string{}
		for ref := range all {
			if s.taskSnapshot.files[ref].Type != "file" {
				return nil, nil, s.Reject("TASK_SNAPSHOT", "封存遗漏来源文件: "+ref)
			}
			out = append(out, ref)
		}
		sort.Strings(out)
		return s.taskSnapshot.proof, out, nil
	}
	if s.GovernanceVersion != 2 || s.RuleSession != nil || s.V.Virtual != nil {
		return nil, refs, nil
	}
	tasks := []any{}
	commits := map[string][]string{}
	all := map[string]bool{}
	for _, ref := range refs {
		all[ref] = true
	}
	for _, ref := range refs {
		doc, err := snapshotTaskDocument(s, ref)
		if err != nil {
			return nil, nil, err
		}
		if ApNumber(doc["schema_version"]) != 3 || ApMap(doc["contract"])["kind"] != "lifecycle-work-unit" || doc["workflow_status"] != "resolved" {
			continue
		}
		if ApMap(doc["baseline"])["repository_root"] != s.Root {
			return nil, nil, s.Reject("TASK_SNAPSHOT", "来源生命周期任务不能借用外部工程")
		}
		if err = s.Verify("task", ref, nil); err != nil {
			return nil, nil, err
		}
		bound, err := s.Bind(ref)
		if err != nil {
			return nil, nil, err
		}
		tasks = append(tasks, map[string]any{"ref": ref, "digest": strings.TrimPrefix(bound.Digest, "sha256:")})
		commit := Text(ApMap(doc["baseline"])["commit"])
		commits[commit] = append(commits[commit], ApStrings(doc["allowed_write_paths"])...)
		for _, scope := range ApStrings(doc["allowed_write_paths"]) {
			files, err := s.Scan(scope)
			if err != nil {
				return nil, nil, err
			}
			for _, file := range files {
				all[file] = true
			}
		}
		for _, bucket := range []any{doc["expected_outputs"], doc["expected_evidence_files"], ApMap(doc["result"])["evidence_refs"]} {
			for _, file := range ApStrings(bucket) {
				all[file] = true
			}
		}
	}
	if len(tasks) == 0 {
		return nil, refs, nil
	}
	objects := map[string]snapshotGitObject{}
	var loadTree func(string) error
	loadTree = func(oid string) error {
		if objects[oid].kind != "" {
			return nil
		}
		raw, err := s.Git(s.Root, "cat-file", "tree", oid)
		if err != nil {
			return err
		}
		objects[oid] = snapshotGitObject{"tree", raw}
		// Load directory objects before flattening; no source code or Git hooks are run.
		for len(raw) > 0 {
			space, nul := bytes.IndexByte(raw, ' '), bytes.IndexByte(raw, 0)
			if space < 1 || nul <= space || len(raw) < nul+21 {
				return s.Reject("TASK_SNAPSHOT", "原Git tree格式无效")
			}
			mode := string(raw[:space])
			child := hex.EncodeToString(raw[nul+1 : nul+21])
			raw = raw[nul+21:]
			if mode == "40000" {
				if err = loadTree(child); err != nil {
					return err
				}
			}
		}
		if len(objects) > 20000 {
			return s.Reject("TASK_SNAPSHOT", "Git对象数量超限")
		}
		return nil
	}
	for commit, scopes := range commits {
		raw, err := s.Git(s.Root, "cat-file", "-p", commit)
		if err != nil {
			return nil, nil, err
		}
		objects[commit] = snapshotGitObject{"commit", raw}
		line := strings.SplitN(string(raw), "\n", 2)[0]
		if !strings.HasPrefix(line, "tree ") || len(line) != 45 {
			return nil, nil, s.Reject("TASK_SNAPSHOT", "原commit缺少tree")
		}
		tree := strings.TrimPrefix(line, "tree ")
		if err = loadTree(tree); err != nil {
			return nil, nil, err
		}
		_, err = snapshotTreeRows(s, objects, tree, "", 0, func(ref, mode, oid string) error {
			inside := false
			for _, scope := range scopes {
				inside = inside || TaskWithinPath(ref, strings.TrimSuffix(scope, "/"))
			}
			if !inside {
				return nil
			}
			if !SemHas([]string{"100644", "100755"}, mode) {
				return s.Reject("TASK_SNAPSHOT", "来源范围含链接或gitlink")
			}
			if objects[oid].kind != "" {
				return nil
			}
			raw, err := s.Git(s.Root, "cat-file", "blob", oid)
			if err != nil {
				return err
			}
			objects[oid] = snapshotGitObject{"blob", raw}
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
	}
	objectRows := []any{}
	for oid, object := range objects {
		objectRows = append(objectRows, map[string]any{"oid": oid, "type": object.kind, "data_base64": base64.StdEncoding.EncodeToString(object.raw)})
	}
	sort.Slice(objectRows, func(i, j int) bool { return Text(ApMap(objectRows[i])["oid"]) < Text(ApMap(objectRows[j])["oid"]) })
	out := []string{}
	for ref := range all {
		out = append(out, ref)
	}
	sort.Strings(out)
	files := []any{}
	for _, ref := range out {
		d, err := s.V.Watch(s.LocalRef(ref))
		if err != nil {
			return nil, nil, err
		}
		if d.Type != "file" {
			return nil, nil, s.Reject("TASK_SNAPSHOT", "来源产物或证据缺失: "+ref)
		}
		files = append(files, map[string]any{"ref": ref, "digest": d.Digest, "mode": d.Mode})
	}
	return map[string]any{"schema_version": 1, "repository_root": s.Root, "tasks": tasks, "objects": objectRows, "files": files}, out, nil
}

// This is a logical source identity; it never selects a physical receiver root.
func snapshotOriginRoot(root string) bool {
	if strings.ContainsAny(root, "\x00\r\n") {
		return false
	}
	wire := strings.ReplaceAll(root, "\\", "/")
	drive := len(wire) > 2 && ((wire[0] >= 'A' && wire[0] <= 'Z') || (wire[0] >= 'a' && wire[0] <= 'z')) && wire[1:3] == ":/"
	if !strings.HasPrefix(wire, "/") && !drive {
		return false
	}
	for _, part := range strings.Split(wire, "/") {
		if part == "." || part == ".." {
			return false
		}
	}
	return true
}

func snapshotTaskDocument(s *SemanticSession, ref string) (map[string]any, error) {
	if !strings.HasSuffix(ref, ".json") {
		return nil, nil
	}
	raw, err := s.Bytes(ref)
	if err != nil {
		return nil, err
	}
	value, err := schema.Parse(raw)
	if err != nil {
		return nil, err
	}
	doc, _ := Object(value)
	return doc, nil
}
