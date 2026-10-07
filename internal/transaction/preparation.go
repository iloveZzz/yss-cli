package transaction

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

// The header is durable before .preparing is created. Publication (the atomic
// rename to a transaction ID) remains the sole permission to write targets.
// A preparation seal records preserved evidence; it is never a recovery journal.
type preparationHeader struct {
	SchemaVersion int    `json:"schemaVersion"`
	Profile       string `json:"profile,omitempty"`
	Plan          plan   `json:"plan"`
}
type preparationSeal struct {
	SchemaVersion    int                          `json:"schemaVersion"`
	ID               string                       `json:"id"`
	Root             string                       `json:"root"`
	Profile          string                       `json:"profile"`
	Kind             string                       `json:"kind"`
	PlanDigest       string                       `json:"planDigest,omitempty"`
	InitialEmpty     bool                         `json:"initialEmpty"`
	BaselineVerified bool                         `json:"baselineVerified"`
	NoTargetWrites   bool                         `json:"noTargetWrites"`
	SealedAt         string                       `json:"sealedAt"`
	Files            map[string]domain.Descriptor `json:"files"`
	Directories      map[string]uint32            `json:"directories"`
}
type preparation struct {
	seal              *preparationSeal
	sealTemps         []string
	id, base, sibling string
	header            *preparationHeader
	plan              *plan
	initial           bool
	files             map[string]domain.Descriptor
	dirs              map[string]uint32
}

func isProjectTransaction(kind string) bool {
	switch kind {
	case "init", "attach", "sync", "migrate", "skills", "assets":
		return true
	default:
		return false
	}
}
func headerRef(id string) string { return stateRef + "/.preparation-" + id + ".json" }
func sealRef(id string) string   { return stateRef + "/.sealed-preparation-" + id }
func headerID(name string) (string, bool) {
	if !strings.HasPrefix(name, ".preparation-") {
		return "", false
	}
	rest := strings.TrimPrefix(name, ".preparation-")
	if len(rest) < 37 || rest[32:37] != ".json" || !validID(rest[:32]) {
		return "", false
	}
	if len(rest) > 37 && !(strings.HasPrefix(rest[37:], ".writing-") && validID(strings.TrimPrefix(rest[37:], ".writing-"))) {
		return "", false
	}
	return rest[:32], true
}
func writingName(name, base string) bool {
	return strings.HasPrefix(name, base+".writing-") && validID(strings.TrimPrefix(name, base+".writing-"))
}
func validDigest(name string) bool {
	b, e := hex.DecodeString(name)
	return e == nil && len(b) == 32 && name == strings.ToLower(name)
}

// inventory does not follow links and retains the actual permission bits.
func preparationInventory(root, base string) (map[string]domain.Descriptor, map[string]uint32, error) {
	files := map[string]domain.Descriptor{}
	dirs := map[string]uint32{}
	p, e := safefs.Path(root, base)
	if e != nil {
		return nil, nil, e
	}
	info, e := os.Lstat(p)
	if os.IsNotExist(e) {
		return files, dirs, nil
	}
	if e != nil {
		return nil, nil, e
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, nil, fail("STATE", "准备状态不是普通目录")
	}
	e = filepath.WalkDir(p, func(path string, entry os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		ref, e := filepath.Rel(p, path)
		if e != nil {
			return e
		}
		ref = filepath.ToSlash(ref)
		info, e := os.Lstat(path)
		if e != nil {
			return e
		}
		if entry.IsDir() {
			if ref != "." && ref != "objects" {
				return fail("STATE", "准备目录含未知资产: "+ref)
			}
			dirs[ref] = uint32(info.Mode().Perm())
			return nil
		}
		if !info.Mode().IsRegular() {
			return fail("STATE", "准备目录含链接或特殊文件: "+ref)
		}
		if ref == "seal.json" || writingName(ref, "seal.json") {
			return nil
		}
		b, e := read(root, base+"/"+ref)
		if e != nil {
			return e
		}
		files[ref] = domain.Descriptor{Type: "file", Digest: safefs.Digest(b), Mode: uint32(info.Mode().Perm())}
		return nil
	})
	return files, dirs, e
}
func allowedPreparationFile(ref string) bool {
	for _, name := range []string{"plan.json", "journal.json", "intent.wal", "header.json"} {
		if ref == name || writingName(ref, name) {
			return true
		}
	}
	if strings.HasPrefix(ref, "seal-partial-") {
		return validID(strings.TrimPrefix(ref, "seal-partial-"))
	}
	if strings.HasPrefix(ref, "header-partial-") {
		return validID(strings.TrimPrefix(ref, "header-partial-"))
	}
	if strings.HasPrefix(ref, "objects/") {
		name := strings.TrimPrefix(ref, "objects/")
		if validDigest(name) {
			return true
		}
		pos := strings.Index(name, ".writing-")
		return pos == 64 && validDigest(name[:pos]) && validID(name[pos+9:])
	}
	return false
}
func readPreparationHeader(root, ref, id string) (*preparationHeader, error) {
	var h preparationHeader
	info, e := os.Lstat(filepath.Join(root, filepath.FromSlash(ref)))
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() || domain.FileMode(uint32(info.Mode().Perm())) != domain.FileMode(0600) {
		return nil, fail("STATE", "准备头类型或权限未知")
	}
	if e := readJSON(root, ref, &h); e != nil {
		return nil, e
	}
	if h.SchemaVersion != 1 {
		return nil, fail("STATE", "准备头 schema 未知")
	}
	if e := validatePlan(root, id, h.Plan); e != nil {
		return nil, e
	}
	if h.Profile != "" {
		if _, e := domain.GetProfile(h.Profile); e != nil {
			return nil, fail("STATE", "准备头 Profile 未知")
		}
	}
	return &h, nil
}

// VerifyEmptyPreparationRoot rejects every target asset and unknown runtime root.
func VerifyEmptyPreparationRoot(root string) error { return emptyPreparationRoot(root) }
func emptyPreparationRoot(root string) error {
	entries, e := os.ReadDir(root)
	if e != nil {
		return e
	}
	if len(entries) != 1 || entries[0].Name() != ".yss" || !entries[0].IsDir() {
		return fail("CONCURRENT", "旧不完整 init 准备只允许严格空目标；保留用户资产")
	}
	entries, e = os.ReadDir(filepath.Join(root, ".yss"))
	if e != nil {
		return e
	}
	if len(entries) != 1 || entries[0].Name() != "transactions" || !entries[0].IsDir() {
		return fail("STATE", ".yss 含未知状态资产，不能作为空初始化准备")
	}
	return nil
}

func loadPreparation(root, id string) (preparation, error) {
	c := preparation{id: id, base: stateRef + "/.preparing-" + id}
	files, dirs, e := preparationInventory(root, c.base)
	if e != nil {
		return c, e
	}
	c.files, c.dirs = files, dirs
	for ref, d := range files {
		if !allowedPreparationFile(ref) {
			return c, fail("STATE", "准备状态含未知资产: "+ref)
		}
		if domain.FileMode(d.Mode) != domain.FileMode(0600) {
			return c, fail("STATE", "准备状态权限未知: "+ref)
		}
	}
	if _, ok := files["plan.json"]; ok {
		var p plan
		if e := readJSON(root, c.base+"/plan.json", &p); e != nil {
			return c, e
		}
		if e := validatePlan(root, id, p); e != nil {
			return c, e
		}
		c.plan = &p
	}
	if _, ok := files["header.json"]; ok {
		c.header, e = readPreparationHeader(root, c.base+"/header.json", id)
		if e != nil {
			return c, e
		}
	}
	entries, e := os.ReadDir(filepath.Join(root, filepath.FromSlash(stateRef)))
	if e != nil {
		return c, e
	}
	for _, entry := range entries {
		candidate, ok := headerID(entry.Name())
		if !ok || candidate != id {
			continue
		}
		if c.sibling != "" || c.header != nil {
			return c, fail("STATE", "准备头重复")
		}
		c.sibling = stateRef + "/" + entry.Name()
		if entry.Name() == ".preparation-"+id+".json" {
			c.header, e = readPreparationHeader(root, c.sibling, id)
			if e != nil {
				return c, e
			}
		} else {
			if _, e := read(root, c.sibling); e != nil {
				return c, e
			}
			info, e := os.Lstat(filepath.Join(root, filepath.FromSlash(c.sibling)))
			if e != nil {
				return c, e
			}
			if domain.FileMode(uint32(info.Mode().Perm())) != domain.FileMode(0600) {
				return c, fail("STATE", "部分准备头权限未知")
			}
		}
	}
	if c.header != nil {
		if c.plan != nil && safefs.Digest(encode(*c.plan)) != safefs.Digest(encode(c.header.Plan)) {
			return c, fail("STATE", "准备头与计划不一致")
		}
		c.plan = &c.header.Plan
	}
	if c.plan == nil {
		for ref := range files {
			if !(writingName(ref, "plan.json") || strings.HasPrefix(ref, "header-partial-") || strings.HasPrefix(ref, "seal-partial-")) {
				return c, fail("STATE", "无完整计划的 preparing 含不可能的后续阶段资产")
			}
		}
		c.initial = emptyPreparationRoot(root) == nil
		if c.sibling != "" && c.header == nil && len(dirs) > 0 && (len(dirs) != 1 || len(files) != 0) {
			return c, fail("STATE", "未完成准备头与非空 preparing 并存")
		}
	} else {
		if _, ok := files["journal.json"]; ok {
			var j journal
			if e := readJSON(root, c.base+"/journal.json", &j); e != nil {
				return c, e
			}
			if j.SchemaVersion != 1 || j.ID != id || j.Phase != "prepared" || j.PlanDigest != safefs.Digest(encode(*c.plan)) {
				return c, fail("STATE", "未发布准备的 journal 不是 prepared")
			}
		}
	}
	if _, ok := files["intent.wal"]; ok {
		b, e := read(root, c.base+"/intent.wal")
		if e != nil {
			return c, e
		}
		if len(b) != 0 {
			return c, fail("STATE", "准备 WAL 已有写入意图，拒绝封存")
		}
	}
	expected := map[string]bool{}
	if c.plan != nil {
		for _, a := range c.plan.Artifacts {
			expected[a.Descriptor.Digest] = true
		}
		for _, r := range c.plan.Operations {
			if r.Before.Type == "file" {
				expected[r.Before.Digest] = true
			}
			if r.After.Type == "file" {
				expected[r.After.Digest] = true
			}
		}
	}
	for ref, d := range files {
		if strings.HasPrefix(ref, "objects/") {
			name := strings.TrimPrefix(ref, "objects/")
			if !validDigest(name) && c.plan != nil {
				pos := strings.Index(name, ".writing-")
				if pos != 64 || !expected[name[:pos]] {
					return c, fail("STATE", "部分准备 object 来源未知")
				}
			}
			if validDigest(name) {
				if c.plan != nil && !expected[name] {
					return c, fail("STATE", "准备 object 摘要或来源矛盾")
				}
				// Earlier producers opened the canonical digest name before
				// writing its bytes. Such a killed write is control evidence,
				// never a valid object for published recovery. Accept it only
				// with the complete durable header's expected source; the empty
				// WAL and all target Before/Guards are also verified before seal.
				if d.Digest != name && (c.header == nil || c.plan == nil || !expected[name]) {
					return c, fail("STATE", "准备 object 摘要或来源矛盾")
				}
			}
		}
	}
	// A complete seal left before rename must be our own exact durable receipt.
	// A partial receipt is retained verbatim under a distinct name before a retry.
	if entries, e := os.ReadDir(filepath.Join(root, filepath.FromSlash(c.base))); e == nil {
		for _, entry := range entries {
			if writingName(entry.Name(), "seal.json") {
				b, e := read(root, c.base+"/"+entry.Name())
				_ = b
				if e != nil {
					return c, e
				}
				info, e := entry.Info()
				if e != nil {
					return c, e
				}
				if domain.FileMode(uint32(info.Mode().Perm())) != domain.FileMode(0600) {
					return c, fail("STATE", "部分封存收据权限未知")
				}
				c.sealTemps = append(c.sealTemps, entry.Name())
			}
			if entry.Name() == "seal.json" {
				if c.sibling != "" {
					return c, fail("STATE", "封存收据与未归档准备头并存")
				}
				var receipt preparationSeal
				if e := readJSON(root, c.base+"/seal.json", &receipt); e != nil {
					return c, e
				}
				if e := validateSeal(root, c.id, receipt, c.files, c.dirs); e != nil {
					return c, e
				}
				if receipt.BaselineVerified != (c.plan != nil) {
					return c, fail("STATE", "封存收据基线证明矛盾")
				}
				if c.plan != nil && (receipt.Kind != c.plan.Kind || receipt.PlanDigest != safefs.Digest(encode(*c.plan))) {
					return c, fail("STATE", "封存收据与原始计划不匹配")
				}
				c.seal = &receipt
			}
		}
	} else if !os.IsNotExist(e) {
		return c, e
	}
	if c.seal != nil && len(c.sealTemps) > 0 {
		return c, fail("STATE", "完整封存收据与部分收据并存")
	}

	return c, nil
}
func validatePreparation(root, profile string, c *preparation, validate ScopeValidator) error {
	if c.seal != nil && c.seal.Profile != profile {
		return fail("IDENTITY", "封存收据与请求 Profile 不匹配")
	}
	if c.header != nil && c.header.Profile != "" && c.header.Profile != profile {
		return fail("IDENTITY", "准备头与请求 Profile 不匹配")
	}
	s := Summary{TransactionID: c.id, Kind: "abandoned-preparation", Phase: "preparing"}
	if c.header != nil {
		s.Profile = c.header.Profile
	}
	paths := []string{}
	if c.plan != nil {
		p := *c.plan
		s.Kind = p.Kind
		s.Sequence = p.Sequence
		s.CreatedAt = p.CreatedAt
		s.PlanDigest = safefs.Digest(encode(p))
		s.Operations = len(p.Operations)
		if e := checkGuards(root, p.Guards, nil); e != nil {
			return e
		}
		for _, r := range p.Operations {
			if e := assertCurrent(root, r, r.Before); e != nil {
				return e
			}
			paths = append(paths, r.Path)
		}
	}
	if validate == nil {
		return fail("ARGUMENT", "准备恢复必须提供身份/范围校验器")
	}
	return validate(s, paths)
}
func validateSeal(root, id string, s preparationSeal, files map[string]domain.Descriptor, dirs map[string]uint32) error {
	if s.SchemaVersion != 1 || s.SealedAt == "" || s.ID != id || s.Root != root || s.Kind == "" || s.Files == nil || s.Directories == nil || !s.NoTargetWrites {
		return fail("STATE", "封存准备身份无效")
	}
	if _, e := domain.GetProfile(s.Profile); e != nil {
		return fail("STATE", "封存准备 Profile 未知")
	}
	if len(files) != len(s.Files) || len(dirs) != len(s.Directories) {
		return fail("STATE", "封存准备清单变化")
	}
	for ref, d := range files {
		if !allowedPreparationFile(ref) || d != s.Files[ref] {
			return fail("STATE", "封存准备字节或权限变化: "+ref)
		}
	}
	for ref, m := range dirs {
		if m != s.Directories[ref] {
			return fail("STATE", "封存准备目录权限变化")
		}
	}
	return nil
}
func preparationSeals(root string) ([]preparationSeal, error) {
	entries, e := os.ReadDir(filepath.Join(root, filepath.FromSlash(stateRef)))
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	seals := []preparationSeal{}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".sealed-preparation-") {
			continue
		}
		id := strings.TrimPrefix(entry.Name(), ".sealed-preparation-")
		if !validID(id) || !entry.IsDir() {
			return nil, fail("STATE", "封存准备目录无效")
		}
		var s preparationSeal
		base := sealRef(id)
		if e := readJSON(root, base+"/seal.json", &s); e != nil {
			return nil, e
		}
		files, dirs, e := preparationInventory(root, base)
		if e != nil {
			return nil, e
		}
		if e := validateSeal(root, id, s, files, dirs); e != nil {
			return nil, e
		}
		entries, e := os.ReadDir(filepath.Join(root, filepath.FromSlash(base)))
		if e != nil {
			return nil, e
		}
		for _, entry := range entries {
			if writingName(entry.Name(), "seal.json") {
				return nil, fail("STATE", "封存归档含未登记收据")
			}
		}

		seals = append(seals, s)
	}
	sort.Slice(seals, func(i, j int) bool { return seals[i].ID < seals[j].ID })
	return seals, nil
}

// SealedInitialization verifies immutable, explicitly-profiled empty init seals.
// It grants no permission to overwrite any target, business file or user state.
func SealedInitialization(root, profile string) (bool, error) {
	root, e := safeRoot(root, false)
	if e != nil {
		return false, e
	}
	seals, e := preparationSeals(root)
	if e != nil {
		return false, e
	}
	if len(seals) == 0 {
		return false, nil
	}
	for _, s := range seals {
		if s.Profile != profile || !s.InitialEmpty || s.Kind != "init" {
			return false, fail("IDENTITY", "封存准备不能作为本 Profile 空初始化历史")
		}
	}
	if e := emptyPreparationRoot(root); e != nil {
		return false, e
	}
	return true, nil
}

// RecoverPreparations preserves an unpublished preparation under its exclusive
// writer lock. Preview is strictly read-only. All current Before/Guards and the
// caller's project identity/scope are checked again under the lock before seal.
func RecoverPreparations(ctx context.Context, root, profile string, apply bool, validate ScopeValidator) (Result, bool, error) {
	if ctx == nil || strings.TrimSpace(root) == "" {
		return Result{}, false, fail("ARGUMENT", "准备恢复必须显式提供 context/root")
	}
	if e := ctx.Err(); e != nil {
		return Result{Status: "cancelled"}, false, domain.Wrap("CANCELLED", e)
	}
	if _, e := domain.GetProfile(profile); e != nil {
		return Result{}, false, e
	}
	root, e := safeRoot(root, false)
	if e != nil {
		return Result{}, false, e
	}
	inspect := func() (Result, []preparation, error) {
		all, refs, e := scan(root)
		if e != nil {
			return Result{}, nil, e
		}
		for _, l := range all {
			if !terminal(l.journal.Phase) {
				return Result{}, nil, nil
			}
		}
		seals, e := preparationSeals(root)
		if e != nil {
			return Result{}, nil, e
		}
		out := Result{Status: "unchanged"}
		for _, s := range seals {
			if len(all) == 0 && s.Profile != profile {
				return Result{}, nil, fail("IDENTITY", "封存历史与请求 Profile 冲突")
			}
			out.SealedPreparations = append(out.SealedPreparations, ".sealed-preparation-"+s.ID)
		}
		if len(refs) > 1 {
			return out, nil, fail("STATE", "多个未发布准备，不能猜测封存顺序")
		}
		candidates := []preparation{}
		for _, ref := range refs {
			id := strings.TrimPrefix(ref, ".preparing-")
			c, e := loadPreparation(root, id)
			if e != nil {
				return out, nil, e
			}
			if e := validatePreparation(root, profile, &c, validate); e != nil {
				return out, nil, e
			}
			candidates = append(candidates, c)
		}
		if len(candidates) > 0 {
			out.Status = "preparing"
			out.Preparations = refs
		}
		return out, candidates, nil
	}
	out, candidates, e := inspect()
	if e != nil {
		return out, true, e
	}
	handled := len(candidates) > 0 || len(out.SealedPreparations) > 0
	if !apply || len(candidates) == 0 {
		return out, handled, nil
	}
	if e := ctx.Err(); e != nil {
		return Result{Status: "cancelled"}, true, domain.Wrap("CANCELLED", e)
	}
	release, e := acquire(root)
	if e != nil {
		return Result{}, true, e
	}
	defer release()
	out, candidates, e = inspect()
	if e != nil {
		return out, true, e
	}
	if e := ctx.Err(); e != nil {
		return Result{Status: "cancelled"}, true, domain.Wrap("CANCELLED", e)
	}
	for _, c := range candidates {
		if e := ensureParents(root, c.base+"/seal.json"); e != nil {
			return out, true, e
		}
		if c.sibling != "" {
			ref := "header.json"
			if c.header == nil {
				ref = "header-partial-" + strings.Split(c.sibling, ".writing-")[1]
			}
			from, e := safefs.Path(root, c.sibling)
			if e != nil {
				return out, true, e
			}
			to, e := safefs.Path(root, c.base+"/"+ref)
			if e != nil {
				return out, true, e
			}
			if _, e := os.Lstat(to); !os.IsNotExist(e) {
				return out, true, fail("STATE", "准备头归档目标已存在")
			}
			if e := os.Rename(from, to); e != nil {
				return out, true, e
			}
			if e := syncDirectory(filepath.Dir(from)); e != nil {
				return out, true, e
			}
			if e := syncDirectory(filepath.Dir(to)); e != nil {
				return out, true, e
			}
		}

		for _, name := range c.sealTemps {
			from, e := safefs.Path(root, c.base+"/"+name)
			if e != nil {
				return out, true, e
			}
			to, e := safefs.Path(root, c.base+"/seal-partial-"+strings.TrimPrefix(name, "seal.json.writing-"))
			if e != nil {
				return out, true, e
			}
			if _, e := os.Lstat(to); !os.IsNotExist(e) {
				return out, true, fail("STATE", "部分封存收据归档已存在")
			}
			if e := os.Rename(from, to); e != nil {
				return out, true, e
			}
			if e := syncDirectory(filepath.Dir(to)); e != nil {
				return out, true, e
			}
		}
		files, dirs, e := preparationInventory(root, c.base)
		if e != nil {
			return out, true, e
		}
		kind := "abandoned-preparation"
		digest := ""
		initial := false
		if c.plan != nil {
			kind = c.plan.Kind
			digest = safefs.Digest(encode(*c.plan))
			initial = kind == "init" && emptyPreparationRoot(root) == nil
		} else {
			initial = c.initial
			if initial {
				kind = "init"
			}
		}
		s := preparationSeal{SchemaVersion: 1, ID: c.id, Root: root, Profile: profile, Kind: kind, PlanDigest: digest, InitialEmpty: initial, BaselineVerified: c.plan != nil, NoTargetWrites: true, SealedAt: time.Now().UTC().Format(time.RFC3339Nano), Files: files, Directories: dirs}
		if c.seal == nil {
			if e := durable(root, c.base+"/seal.json", encode(s), 0600); e != nil {
				return out, true, e
			}
		}
		from, e := safefs.Path(root, c.base)
		if e != nil {
			return out, true, e
		}
		to, e := safefs.Path(root, sealRef(c.id))
		if e != nil {
			return out, true, e
		}
		if _, e := os.Lstat(to); !os.IsNotExist(e) {
			return out, true, fail("STATE", "准备封存目标已存在")
		}
		if e := os.Rename(from, to); e != nil {
			return out, true, e
		}
		if e := syncDirectory(filepath.Dir(to)); e != nil {
			return out, true, e
		}
		out.SealedPreparations = append(out.SealedPreparations, filepath.Base(to))
	}
	out.Status = "sealed"
	out.Preparations = nil
	return out, true, nil
}

// Published headers left by a crash between publication and header unlink must
// match the actual archive, and are not treated as unpublished preparations.
func checkPublishedHeader(root, ref, id string, l loaded) error {
	h, e := readPreparationHeader(root, ref, id)
	if e != nil {
		return e
	}
	if safefs.Digest(encode(h.Plan)) != l.journal.PlanDigest {
		return fail("STATE", fmt.Sprintf("已发布准备头不匹配: %s", id))
	}
	return nil
}
