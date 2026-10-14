package contracts

import (
	"bytes"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

// Intent metadata never becomes approval evidence. This is only a readonly,
// digest-bound inventory of the current feature's verified transaction archive.
func ProgressionIntentMaterials(s *kernel.SemanticSession, cpRef, configRef string, cp map[string]any) (map[string]any, error) {
	materials, err := transaction.LifecycleTargetMaterials(s.Root, configRef)
	if err != nil {
		return nil, err
	}
	files := []any{}
	for _, material := range materials {
		raw, err := s.Bytes(material.Ref)
		if err != nil {
			return nil, err
		}
		descriptor, err := s.V.Watch(material.Ref)
		if err != nil || descriptor.Type != "file" || descriptor.Digest != material.Descriptor.Digest || descriptor.Mode != domain.FileMode(material.Descriptor.Mode) || safefs.Digest(raw) != material.Descriptor.Digest {
			return nil, s.Unavailable("INPUT_DRIFT", "推进目标事务材料在读取期间变化")
		}
		files = append(files, map[string]any{"ref": material.Ref, "digest": "sha256:" + material.Descriptor.Digest, "mode": material.Descriptor.Mode})
	}
	// Recheck the native archive inventory as well as individual observed bytes;
	// an added unknown member must not pass the final input observation.
	if err = s.ObserveOnline("lifecycle-target-materials:"+configRef+":"+ContractDigest(files), func() error {
		current, err := transaction.LifecycleTargetMaterials(s.Root, configRef)
		if err != nil || !reflect.DeepEqual(current, materials) {
			return s.Unavailable("INPUT_DRIFT", "核验结束时推进目标事务库存变化")
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return map[string]any{"schema_version": 1, "kind": "lifecycle-target-intent-materials", "root": s.Root, "feature_id": cp["feature_id"], "checkpoint_ref": cpRef, "config_ref": configRef, "files": files}, nil
}

// Only an exact current feature registration can identify intent metadata.
// Source code, parent build files and all other transactions remain protected
// by the original complete Git candidate; no directory is globally excluded.
func CandidateIntentAllowlist(s *kernel.SemanticSession, root, assetRef string) (map[string]bool, error) {
	allowed := map[string]bool{}
	if assetRef == "" || s.V.Virtual != nil {
		return allowed, nil
	}
	gitRoot, err := s.Git(root, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	if filepath.Clean(strings.TrimSpace(string(gitRoot))) != s.Root {
		return allowed, nil
	}
	present, err := s.Exists(domain.MetadataFile)
	if err != nil || !present {
		return allowed, err
	}
	metadata, err := s.Doc(domain.MetadataFile)
	if err != nil {
		return nil, err
	}
	if metadata["profile"] != "spec" {
		return allowed, nil
	}
	contract, err := s.Doc(kernel.GuidanceContractRef("spec"))
	if err != nil {
		return nil, err
	}
	if contract["progression_target"] == nil {
		return allowed, nil // Historical instances preserve their original guard.
	}
	if err = kernel.ProjectIdentity(s.V); err != nil {
		return nil, err
	}
	profile, policy, err := kernel.ProgressionPolicy(s)
	if err != nil {
		return nil, err
	}
	if profile != "spec" || !kernel.SemHas(policy["writer_profiles"], "spec") {
		return allowed, nil
	}
	cpRef, err := dependencies.ProgressionTaskAssetCheckpoint(s, assetRef)
	if err != nil || cpRef == "" {
		return allowed, err
	}
	configRef, cp, err := kernel.ProgressionLocation(s, cpRef)
	if err != nil {
		return nil, err
	}
	if cp["profile_id"] != nil && cp["profile_id"] != domain.Profiles[profile].ID {
		return nil, s.Reject("IDENTITY", "实现候选功能checkpoint Profile不符")
	}
	if present, err = s.Exists(configRef); err != nil {
		return nil, err
	} else if present {
		raw, err := s.Bytes(configRef)
		if err != nil {
			return nil, err
		}
		target, err := dependencies.ParseProgressionTarget(raw)
		if err != nil {
			return nil, err
		}
		if target.FeatureID != cp["feature_id"] || target.CheckpointRef != cpRef || len(kernel.SemMap(kernel.SemMap(policy["completion_policy"])[target.Target])) == 0 {
			return nil, s.Reject("PROGRESSION_BINDING", "实现候选目标意图未绑定当前功能")
		}
		if err = dependencies.ProgressionScope(s, target.Target); err != nil {
			return nil, err
		}
	}
	materials, err := ProgressionIntentMaterials(s, cpRef, configRef, cp)
	if err != nil {
		return nil, err
	}
	if present {
		allowed[configRef] = true
	}
	for _, item := range kernel.SemList(materials["files"]) {
		allowed[kernel.Text(kernel.SemMap(item)["ref"])] = true
	}
	return allowed, nil
}

func candidateOnlyIntentPaths(s *kernel.SemanticSession, paths []byte, allowed map[string]bool, code string) error {
	for _, ref := range strings.Split(string(paths), "\x00") {
		if ref != "" && !allowed[ref] {
			return s.Reject(code, "实现候选存在未批准的当前变化："+ref)
		}
	}
	return nil
}

func CandidateCommittedCurrent(s *kernel.SemanticSession, root, candidate, digest, assetRef, code string) (map[string]bool, error) {
	if !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(candidate) || !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(digest) {
		return nil, s.Reject(code, "提交候选必须保留原固定提交及tree身份")
	}
	tree, err := s.Git(root, "rev-parse", candidate+"^{tree}")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(tree)) != digest {
		return nil, s.Reject(code, "原提交候选tree身份不符")
	}
	allowed, err := CandidateIntentAllowlist(s, root, assetRef)
	if err != nil {
		return nil, err
	}
	head, err := s.Git(root, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(tree, head) {
		paths, err := s.Git(root, "diff", "--no-renames", "--name-only", "-z", candidate, "HEAD")
		if err != nil {
			return nil, err
		}
		if err = candidateOnlyIntentPaths(s, paths, allowed, code); err != nil {
			return nil, err
		}
	}
	// A stale stat cache can make status/name-only report a modification even
	// when file bytes match. Full patches inspect bytes without refreshing the
	// source index; both staged and worktree views remain protected.
	for _, cached := range []bool{false, true} {
		args := []string{"diff", "--binary", "--full-index", "--no-renames"}
		if cached {
			args = append(args, "--cached")
		}
		args = append(args, candidate, "--")
		for _, ref := range BackendSortedKeys(allowed) {
			args = append(args, ":(top,literal,exclude)"+ref)
		}
		patch, err := s.Git(root, args...)
		if err != nil {
			return nil, err
		}
		if len(patch) != 0 {
			return nil, s.Reject(code, "实现候选有未批准的源码或暂存变化")
		}
	}
	untracked, err := s.Git(root, "ls-files", "-z", "--full-name", "--others", "--exclude-standard", "--", ":/")
	if err != nil {
		return nil, err
	}
	if err = candidateOnlyIntentPaths(s, untracked, allowed, code); err != nil {
		return nil, err
	}
	return allowed, nil
}

// Original full binary patches are reconstructed only in a private object DB
// and index. Source .git, index, object store and working files are never written.
func CandidateTrackedIntentDifference(s *kernel.SemanticSession, root, base string, original, current []byte, allowed map[string]bool) error {
	if len(allowed) == 0 {
		return BackendReject(s, "工作树 tracked candidate 变化")
	}
	differences, err := candidatePrivatePatchDifference(s, root, base, original, current, "")
	if err != nil {
		return err
	}
	return candidateOnlyIntentPaths(s, differences, allowed, "BACKEND_DELIVERY")
}

// comparison is used only to derive original Coverage names from the verified
// full patch. A private object DB keeps index stat caches out of that decision.
func candidatePrivatePatchDifference(s *kernel.SemanticSession, root, base string, original, current []byte, comparison string) ([]byte, error) {
	baseBytes, err := s.Git(root, "rev-parse", base+"^{tree}")
	if err != nil {
		return nil, err
	}
	baseTree := strings.TrimSpace(string(baseBytes))
	if !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(baseTree) {
		return nil, BackendReject(s, "原候选基线tree非法")
	}
	comparisonTree := ""
	if comparison != "" {
		raw, err := s.Git(root, "rev-parse", comparison+"^{tree}")
		if err != nil {
			return nil, err
		}
		comparisonTree = strings.TrimSpace(string(raw))
		if !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(comparisonTree) {
			return nil, BackendReject(s, "规范覆盖原比较tree非法")
		}
	}
	pack, err := candidateSourcePack(s, root, baseTree, comparisonTree)
	if err != nil {
		return nil, err
	}
	private, err := os.MkdirTemp("", "yss-candidate-current-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(private)
	run := func(input []byte, args ...string) ([]byte, error) {
		if err := s.Guard(); err != nil {
			return nil, err
		}
		flags := []string{"--no-pager", "--no-optional-locks", "--no-lazy-fetch", "--no-replace-objects", "-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false", "-c", "diff.autoRefreshIndex=false", "-c", "core.attributesFile=", "-c", "diff.external="}
		cmd := exec.CommandContext(s.Ctx, "git", append(flags, args...)...)
		cmd.Dir = private
		for _, value := range os.Environ() {
			if !strings.HasPrefix(value, "GIT_") && !strings.HasPrefix(value, "PAGER=") {
				cmd.Env = append(cmd.Env, value)
			}
		}
		cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=", "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "GIT_NO_LAZY_FETCH=1", "GIT_NO_REPLACE_OBJECTS=1", "LC_ALL=C", "GIT_DIR="+private, "GIT_INDEX_FILE="+filepath.Join(private, "candidate.index"), "GIT_OBJECT_DIRECTORY="+filepath.Join(private, "objects"))
		cmd.Stdin = bytes.NewReader(input)
		var stdout, stderr kernel.SemanticLimitedBuffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return nil, BackendReject(s, "私有Git原候选重建失败："+stderr.String())
		}
		return stdout.Bytes(), s.Guard()
	}
	if _, err = run(nil, "init", "--bare", private); err != nil {
		return nil, err
	}
	if _, err = run(pack, "index-pack", "--stdin"); err != nil {
		return nil, err
	}
	reconstruct := func(patch []byte) (string, error) {
		if _, err := run(nil, "read-tree", baseTree); err != nil {
			return "", err
		}
		if len(patch) > 0 {
			if _, err := run(patch, "apply", "--cached", "--binary", "--whitespace=nowarn", "-"); err != nil {
				return "", err
			}
		}
		tree, err := run(nil, "write-tree")
		return strings.TrimSpace(string(tree)), err
	}
	oldTree, err := reconstruct(original)
	if err != nil {
		return nil, err
	}
	if comparisonTree != "" {
		// Preserve original Coverage path spelling and rename behavior. This is
		// a derived record field, not the protected-path authorization check.
		return run(nil, "diff", "--no-ext-diff", "--no-textconv", "--name-only", comparisonTree, oldTree)
	}
	newTree, err := reconstruct(current)
	if err != nil {
		return nil, err
	}
	return run(nil, "diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--name-only", "-z", oldTree, newTree)
}

func CandidateBuildSource(s *kernel.SemanticSession, root string, input map[string]any) (string, error) {
	head, err := s.Git(root, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	allowed, err := CandidateIntentAllowlist(s, root, kernel.Text(input["slice_contract_ref"]))
	if err != nil {
		return "", err
	}
	if len(allowed) > 0 {
		return kernel.Text(input["implementation_candidate_ref"]), nil
	}
	return strings.TrimSpace(string(head)), nil
}

type CandidateCoverageContext struct {
	Allowed, Local               map[string]bool
	Prefix, SourceHead           string
	SnapshotRef, CandidateDigest string
	ReviewMode                   string
}

func CandidateCoverageIntent(s *kernel.SemanticSession, root string, input, recorded map[string]any) (*CandidateCoverageContext, error) {
	if input["scope_kind"] != "change" {
		return nil, nil
	}
	allowed, err := CandidateIntentAllowlist(s, root, kernel.Text(input["slice_contract_ref"]))
	if err != nil {
		return nil, err
	}
	if len(allowed) == 0 {
		return nil, nil
	}
	sourceHead := kernel.Text(input["implementation_candidate_ref"])
	if input["review_mode"] == "worktree" {
		sourceHead = kernel.Text(recorded["source_head"])
	} else if input["review_mode"] != "committed" {
		return nil, BackendReject(s, "未知覆盖候选模式")
	}
	if !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(sourceHead) || recorded["source_head"] != sourceHead {
		return nil, BackendReject(s, "规范覆盖未绑定原已签署源码提交")
	}
	commit, err := s.Git(root, "rev-parse", sourceHead+"^{commit}")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(commit)) != sourceHead {
		return nil, BackendReject(s, "规范覆盖固定提交身份不符")
	}
	paths, err := s.Git(root, "diff", "--no-renames", "--name-only", "-z", sourceHead, "HEAD")
	if err != nil {
		return nil, err
	}
	if err = candidateOnlyIntentPaths(s, paths, allowed, "BACKEND_DELIVERY"); err != nil {
		return nil, err
	}
	gitRoot, err := s.Git(root, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	prefix, err := filepath.Rel(strings.TrimSpace(string(gitRoot)), root)
	if err != nil {
		return nil, err
	}
	prefix = filepath.ToSlash(prefix)
	if prefix == "." {
		prefix = ""
	} else {
		prefix += "/"
	}
	local := map[string]bool{}
	for ref := range allowed {
		if strings.HasPrefix(ref, prefix) {
			local[strings.TrimPrefix(ref, prefix)] = true
		}
	}
	return &CandidateCoverageContext{Allowed: allowed, Local: local, Prefix: prefix, SourceHead: sourceHead, SnapshotRef: kernel.Text(input["candidate_snapshot_ref"]), CandidateDigest: kernel.Text(input["candidate_digest"]), ReviewMode: kernel.Text(input["review_mode"])}, nil
}

func CandidateCoverageTrackedPaths(s *kernel.SemanticSession, root, comparison string, intent *CandidateCoverageContext) ([]byte, error) {
	if intent.ReviewMode == "committed" {
		return s.Git(root, "diff", "--name-only", comparison, intent.SourceHead)
	}
	if intent.ReviewMode != "worktree" || intent.SnapshotRef == "" {
		return nil, BackendReject(s, "规范覆盖缺少原工作树候选绑定")
	}
	ps := s
	if root != s.Root {
		ps = kernel.NewSemanticSession(s.Ctx, root, s.Args)
		ps.RuleSession = s
		s.Children = append(s.Children, ps)
	}
	snapshot, err := ps.WorktreeCandidate(intent.SnapshotRef)
	if err != nil {
		return nil, err
	}
	if snapshot.Manifest["candidate_digest"] != intent.CandidateDigest {
		return nil, BackendReject(s, "规范覆盖未绑定原完整工作树候选")
	}
	return candidatePrivatePatchDifference(s, root, kernel.Text(snapshot.Manifest["merge_base"]), snapshot.TrackedDiff, nil, comparison)
}

func CandidateRestoreCoverageMaterials(current, recorded map[string]any, intent *CandidateCoverageContext) {
	inventory := []any{}
	for _, row := range kernel.SemList(current["inventory"]) {
		if !intent.Local[kernel.Text(kernel.SemMap(row)["path"])] {
			inventory = append(inventory, row)
		}
	}
	for _, row := range kernel.SemList(recorded["inventory"]) {
		if intent.Local[kernel.Text(kernel.SemMap(row)["path"])] {
			inventory = append(inventory, row)
		}
	}
	// Match the original source-inventory ordering, including only original
	// metadata rows. No current intent is admitted as code or approval evidence.
	order := map[string]int{}
	for i, row := range kernel.SemList(recorded["inventory"]) {
		order[kernel.Text(kernel.SemMap(row)["path"])] = i
	}
	sort.SliceStable(inventory, func(i, j int) bool {
		return order[kernel.Text(kernel.SemMap(inventory[i])["path"])] < order[kernel.Text(kernel.SemMap(inventory[j])["path"])]
	})
	current["inventory"] = inventory
	changed := kernel.SemStrings(current["changed_paths"])
	for _, ref := range kernel.SemStrings(recorded["changed_paths"]) {
		if intent.Allowed[ref] || intent.Local[ref] {
			changed = append(changed, ref)
		}
	}
	sort.Strings(changed)
	current["changed_paths"] = changed
}

// pack-objects --stdout reads the immutable base tree; a private index-pack
// imports it without alternates, which could freshen loose source objects.
func candidateSourcePack(s *kernel.SemanticSession, root string, trees ...string) ([]byte, error) {
	if err := s.Guard(); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(s.Ctx, "git", "--no-pager", "--no-optional-locks", "--no-lazy-fetch", "--no-replace-objects", "-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false", "-c", "diff.autoRefreshIndex=false", "pack-objects", "--stdout", "--revs")
	cmd.Dir = root
	validTrees := []string{}
	for _, tree := range trees {
		if tree == "" {
			continue
		}
		if !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(tree) {
			return nil, BackendReject(s, "只读打包tree身份非法")
		}
		validTrees = append(validTrees, tree)
	}
	cmd.Stdin = strings.NewReader(strings.Join(validTrees, "\n") + "\n")
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "GIT_") && !strings.HasPrefix(value, "PAGER=") {
			cmd.Env = append(cmd.Env, value)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=", "GIT_OPTIONAL_LOCKS=0", "GIT_NO_LAZY_FETCH=1", "GIT_NO_REPLACE_OBJECTS=1", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	var stdout, stderr kernel.SemanticLimitedBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, s.Reject("GIT_EVIDENCE", "只读原候选tree打包失败："+stderr.String())
	}
	return stdout.Bytes(), s.Guard()
}
