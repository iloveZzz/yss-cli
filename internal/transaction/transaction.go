// Package transaction implements digest-bound, recoverable project file changes.
// Git internals and nested repositories are always outside its write scope.
package transaction

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
)

const stateRef = ".yss/transactions"

type Operation struct {
	Path   string `json:"path"`
	Data   []byte `json:"data,omitempty"`
	Mode   uint32 `json:"mode,omitempty"`
	Delete bool   `json:"delete,omitempty"`
	// Nil means an absent file, never permission to overwrite an unknown baseline.
	Before *domain.Descriptor `json:"before,omitempty"`
}

type Result struct {
	Status        string    `json:"status"`
	TransactionID string    `json:"transactionId,omitempty"`
	Kind          string    `json:"kind,omitempty"`
	PlanDigest    string    `json:"planDigest,omitempty"`
	BackupPath    string    `json:"backupPath,omitempty"`
	Operations    int       `json:"operations,omitempty"`
	Pending       []string  `json:"pending,omitempty"`
	Preparations  []string  `json:"preparations,omitempty"`
	Transactions  []Summary `json:"transactions,omitempty"`
}

type Summary struct {
	TransactionID string `json:"transactionId"`
	Kind          string `json:"kind"`
	Phase         string `json:"phase"`
	PlanDigest    string `json:"planDigest"`
	Operations    int    `json:"operations"`
	CreatedAt     string `json:"createdAt"`
	Sequence      uint64 `json:"sequence"`
}

type record struct {
	Path   string            `json:"path"`
	Before domain.Descriptor `json:"before"`
	After  domain.Descriptor `json:"after"`
}
type plan struct {
	Guards        map[string]domain.Descriptor `json:"guards,omitempty"`
	SchemaVersion int                          `json:"schemaVersion"`
	Sequence      uint64                       `json:"sequence"`
	ID            string                       `json:"id"`
	Root          string                       `json:"root"`
	Kind          string                       `json:"kind"`
	CreatedAt     string                       `json:"createdAt"`
	Operations    []record                     `json:"operations"`
}
type journal struct {
	SchemaVersion int    `json:"schemaVersion"`
	ID            string `json:"id"`
	PlanDigest    string `json:"planDigest"`
	Phase         string `json:"phase"`
	UpdatedAt     string `json:"updatedAt"`
}
type loaded struct {
	plan    plan
	journal journal
	base    string
}
type intent struct {
	SchemaVersion int `json:"schemaVersion"`
	Index         int `json:"index"`
}
type lockRecord struct {
	PID   int    `json:"pid"`
	Host  string `json:"host"`
	Token string `json:"token"`
}

func fail(code, message string) error { return domain.Fail(code, message) }
func same(a, b domain.Descriptor) bool {
	return a.Type == b.Type && a.Digest == b.Digest && a.Mode == b.Mode
}
func identifier() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
func validID(id string) bool {
	b, e := hex.DecodeString(id)
	return e == nil && len(b) == 16 && id == strings.ToLower(id)
}
func encode(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return append(b, '\n')
}
func descriptorValid(d domain.Descriptor) bool {
	if d.Type == "missing" {
		return d.Digest == "" && d.Mode == 0
	}
	b, err := hex.DecodeString(d.Digest)
	return d.Type == "file" && d.Mode <= 0777 && err == nil && len(b) == 32 && d.Digest == strings.ToLower(d.Digest)
}

func normalizedDescriptor(d domain.Descriptor) domain.Descriptor {
	if d.Type == "file" {
		d.Mode = domain.FileMode(d.Mode)
	}
	return d
}
func canonicalDescriptor(d domain.Descriptor) bool {
	return descriptorValid(d) && (d.Type != "file" || d.Mode == domain.FileMode(d.Mode))
}

func safeRoot(root string, create bool) (string, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	cursor := filepath.VolumeName(absolute) + string(filepath.Separator)
	rest := strings.TrimPrefix(absolute, cursor)
	for _, part := range strings.Split(rest, string(filepath.Separator)) {
		if part == "" {
			continue
		}
		cursor = filepath.Join(cursor, part)
		info, e := os.Lstat(cursor)
		if e == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
			return "", fail("PATH", "项目根路径含链接或非目录: "+cursor)
		}
		if e != nil && !os.IsNotExist(e) {
			return "", e
		}
	}
	if create {
		if err := os.MkdirAll(absolute, 0755); err != nil {
			return "", err
		}
	}
	return absolute, nil
}

func guard(root, ref string) error {
	if strings.EqualFold(strings.SplitN(ref, "/", 2)[0], ".yss") {
		return fail("PROTECTED", "事务不能覆盖自己的状态目录: "+ref)
	}
	if _, err := safefs.Path(root, ref); err != nil {
		return err
	}
	parent := filepath.Dir(filepath.FromSlash(ref))
	for parent != "." {
		p, e := safefs.Path(root, filepath.ToSlash(filepath.Join(parent, ".git")))
		if e != nil {
			// safefs intentionally rejects .git; inspect this marker without dereferencing it.
			p = filepath.Join(root, parent, ".git")
		}
		if _, e := os.Lstat(p); e == nil {
			return fail("PROTECTED", "拒绝嵌套 Git 仓库: "+ref)
		} else if !os.IsNotExist(e) {
			return e
		}
		parent = filepath.Dir(parent)
	}
	return nil
}

func normalize(root string, ops []Operation) ([]record, error) {
	records := make([]record, 0, len(ops))
	seen := map[string]bool{}
	var spellings safefs.PathSet
	descendants := map[string]bool{}
	for _, op := range ops {
		if err := guard(root, op.Path); err != nil {
			return nil, err
		}
		if err := spellings.Add(op.Path); err != nil {
			return nil, fail("PLAN", err.Error())
		}
		if seen[op.Path] {
			return nil, fail("PLAN", "重复操作路径: "+op.Path)
		}
		if descendants[op.Path] {
			return nil, fail("PLAN", "操作路径存在父子冲突")
		}
		for parent := filepath.ToSlash(filepath.Dir(op.Path)); parent != "."; parent = filepath.ToSlash(filepath.Dir(parent)) {
			if seen[parent] {
				return nil, fail("PLAN", "操作路径存在父子冲突")
			}
			descendants[parent] = true
		}
		seen[op.Path] = true
		before := domain.Descriptor{Type: "missing"}
		if op.Before != nil {
			before = *op.Before
		}
		if !descriptorValid(before) {
			return nil, fail("PLAN", "无效输入摘要: "+op.Path)
		}
		before = normalizedDescriptor(before)
		mode := op.Mode
		if mode == 0 {
			mode = 0644
			if before.Type == "file" {
				mode = before.Mode
			}
		}
		if mode > 0777 {
			return nil, fail("PLAN", "文件权限超出 0777: "+op.Path)
		}
		mode = domain.FileMode(mode)
		// Replacing an existing Windows read-only destination would require an
		// extra attribute mutation or a native API contract we have not verified.
		// Do not introduce an unjournaled permission intermediate state.
		if runtime.GOOS == "windows" && (before.Type == "file" && before.Mode == 0444 || !op.Delete && mode == 0444) {
			return nil, fail("UNPORTED", "Windows 只读文件删除/替换/只读输出的可恢复原生合同尚未验证: "+op.Path)
		}
		after := domain.Descriptor{Type: "missing"}
		if !op.Delete {
			after = domain.Descriptor{Type: "file", Digest: safefs.Digest(op.Data), Mode: mode}
		}
		records = append(records, record{Path: op.Path, Before: before, After: after})
	}
	return records, nil
}

func assertCurrent(root string, r record, expected domain.Descriptor) error {
	if err := guard(root, r.Path); err != nil {
		return err
	}
	actual, err := safefs.Describe(root, r.Path)
	if err != nil {
		return err
	}
	if !same(actual, expected) {
		return fail("CONCURRENT", "文件内容或权限已变化，未覆盖: "+r.Path)
	}
	return nil
}

func syncDirectory(directory string) error {
	f, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer f.Close()
	return syncDirectoryFile(f)
}

func ensureParents(root, ref string) error {
	p, err := safefs.Path(root, ref)
	if err != nil {
		return err
	}
	parent := filepath.Dir(p)
	missing := []string{}
	for cur := parent; cur != root; cur = filepath.Dir(cur) {
		info, e := os.Lstat(cur)
		if e == nil {
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return fail("PATH", "写入父路径不是安全目录")
			}
			break
		}
		if !os.IsNotExist(e) {
			return e
		}
		missing = append(missing, cur)
	}
	for i := len(missing) - 1; i >= 0; i-- {
		if err := os.Mkdir(missing[i], 0755); err != nil && !os.IsExist(err) {
			return err
		}
		if err := syncDirectory(filepath.Dir(missing[i])); err != nil {
			return err
		}
	}
	return nil
}

func durable(root, ref string, b []byte, mode uint32) error {
	if err := ensureParents(root, ref); err != nil {
		return err
	}
	p, err := safefs.Path(root, ref)
	if err != nil {
		return err
	}
	id, err := identifier()
	if err != nil {
		return err
	}
	temp := p + ".writing-" + id
	f, err := os.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer os.Remove(temp)
	if _, err = f.Write(b); err == nil {
		err = f.Chmod(os.FileMode(mode))
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if _, err = safefs.Path(root, ref); err != nil {
		return err
	}
	if err = os.Rename(temp, p); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(p))
}

func read(root, ref string) ([]byte, error) {
	p, err := safefs.Path(root, ref)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fail("STATE", "状态资产不是普通文件: "+ref)
	}
	return os.ReadFile(p)
}

func readJSON(root, ref string, out any) error {
	b, err := read(root, ref)
	if err != nil {
		return err
	}
	if !json.Valid(b) {
		return fail("STATE", "状态资产必须是单个 JSON 文档: "+ref)
	}
	if _, err := schema.Parse(b); err != nil {
		return fail("STATE", ref+": "+err.Error())
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return fail("STATE", ref+": "+err.Error())
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return fail("STATE", "状态资产含尾随内容: "+ref)
	}
	return nil
}

func update(root string, l *loaded, phase string) error {
	l.journal.Phase = phase
	l.journal.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return durable(root, l.base+"/journal.json", encode(l.journal), 0600)
}

func load(root, id string) (loaded, error) {
	l := loaded{base: stateRef + "/" + id}
	if !validID(id) {
		return l, fail("STATE", "未知事务目录: "+id)
	}
	b, err := read(root, l.base+"/plan.json")
	if err != nil {
		return l, err
	}
	if err := readJSON(root, l.base+"/plan.json", &l.plan); err != nil {
		return l, err
	}
	if err := readJSON(root, l.base+"/journal.json", &l.journal); err != nil {
		return l, err
	}
	if l.plan.SchemaVersion != 2 || l.plan.Sequence == 0 || l.journal.SchemaVersion != 1 || l.plan.ID != id || l.journal.ID != id || l.plan.Root != root || l.journal.PlanDigest != safefs.Digest(b) {
		return l, fail("STATE", "事务身份或计划摘要不匹配: "+id)
	}
	switch l.journal.Phase {
	case "prepared", "applying", "committed", "recovering", "recovered", "rolling-back", "rolled-back":
	default:
		return l, fail("STATE", "未知事务阶段")
	}
	seen := map[string]bool{}
	descendants := map[string]bool{}
	var spellings safefs.PathSet
	for _, r := range l.plan.Operations {
		if err := guard(root, r.Path); err != nil {
			return l, err
		}
		if err := spellings.Add(r.Path); err != nil {
			return l, fail("STATE", err.Error())
		}
		if seen[r.Path] || descendants[r.Path] || !canonicalDescriptor(r.Before) || !canonicalDescriptor(r.After) {
			return l, fail("STATE", "事务操作清单无效")
		}
		for parent := filepath.ToSlash(filepath.Dir(r.Path)); parent != "."; parent = filepath.ToSlash(filepath.Dir(parent)) {
			if seen[parent] {
				return l, fail("STATE", "事务操作存在父子冲突")
			}
			descendants[parent] = true
		}
		if runtime.GOOS == "windows" && (r.Before.Type == "file" && r.Before.Mode == 0444 || r.After.Type == "file" && r.After.Mode == 0444) {
			return l, fail("UNPORTED", "Windows 只读事务恢复合同尚未验证")
		}
		seen[r.Path] = true
	}
	for ref, d := range l.plan.Guards {
		if _, err := safefs.Path(root, ref); err != nil {
			return l, err
		}
		if err := spellings.Add(ref); err != nil {
			return l, fail("STATE", err.Error())
		}
		if !canonicalDescriptor(d) {
			return l, fail("STATE", "事务只读输入摘要无效: "+ref)
		}
	}
	for _, r := range l.plan.Operations {
		if d, ok := l.plan.Guards[r.Path]; ok && !same(d, r.Before) {
			return l, fail("STATE", "事务输入与写入基线矛盾: "+r.Path)
		}
	}
	return l, nil
}

func scan(root string) ([]loaded, []string, error) {
	p, err := safefs.Path(root, stateRef)
	if err != nil {
		return nil, nil, err
	}
	entries, err := os.ReadDir(p)
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	all := []loaded{}
	preparations := []string{}
	for _, entry := range entries {
		name := entry.Name()
		if name == "lock.json" || name == ".lock" || strings.Contains(name, ".writing-") {
			continue
		}
		if strings.HasPrefix(name, ".preparing-") && entry.IsDir() && validID(strings.TrimPrefix(name, ".preparing-")) {
			preparations = append(preparations, name)
			continue
		}
		if !entry.IsDir() {
			return nil, nil, fail("STATE", "未知事务状态资产: "+name)
		}
		l, err := load(root, name)
		if err != nil {
			return nil, nil, err
		}
		all = append(all, l)
	}
	sequences := map[uint64]bool{}
	for _, l := range all {
		if sequences[l.plan.Sequence] {
			return nil, nil, fail("STATE", "事务序号重复，不能猜测执行顺序")
		}
		sequences[l.plan.Sequence] = true
	}
	sort.Slice(all, func(i, j int) bool { return all[i].plan.Sequence < all[j].plan.Sequence })
	return all, preparations, nil
}
func terminal(phase string) bool {
	return phase == "committed" || phase == "recovered" || phase == "rolled-back"
}
func result(l loaded, status string) Result {
	return Result{Status: status, TransactionID: l.plan.ID, Kind: l.plan.Kind, PlanDigest: l.journal.PlanDigest, BackupPath: filepath.Join(l.plan.Root, filepath.FromSlash(l.base)), Operations: len(l.plan.Operations)}
}

func acquire(root string) (func(), error) {
	if err := ensureParents(root, stateRef+"/lock.json"); err != nil {
		return nil, err
	}
	p, err := safefs.Path(root, stateRef+"/.lock")
	if err != nil {
		return nil, err
	}
	hostname, err := os.Hostname()
	if err != nil {
		return nil, err
	}
	token, err := identifier()
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	unlock, err := lockFile(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	content := encode(lockRecord{PID: os.Getpid(), Host: hostname, Token: token})
	if err := durable(root, stateRef+"/lock.json", content, 0600); err != nil {
		unlock()
		f.Close()
		return nil, err
	}
	return func() {
		b, e := read(root, stateRef+"/lock.json")
		if e == nil && bytes.Equal(b, content) {
			diagnostic, _ := safefs.Path(root, stateRef+"/lock.json")
			os.Remove(diagnostic)
			_ = syncDirectory(filepath.Dir(p))
		}
		unlock()
		f.Close()
	}, nil
}

// Apply uses an immutable plan, byte archives, and an fsynced intent WAL.
func Apply(root, kind string, ops []Operation) (Result, error) {
	return ApplyContext(context.Background(), root, kind, ops)
}
func ApplyContext(ctx context.Context, root, kind string, ops []Operation) (Result, error) {
	return ApplyContextWithGuards(ctx, root, kind, ops, nil)
}
func ApplyWithGuards(root, kind string, ops []Operation, guards map[string]domain.Descriptor) (Result, error) {
	return ApplyContextWithGuards(context.Background(), root, kind, ops, guards)
}
func ApplyContextWithGuards(ctx context.Context, root, kind string, ops []Operation, guards map[string]domain.Descriptor) (Result, error) {
	if ctx == nil {
		return Result{}, fail("PLAN", "取消上下文不可为空")
	}
	if err := ctx.Err(); err != nil {
		return Result{Status: "cancelled"}, err
	}
	if strings.TrimSpace(kind) == "" || strings.ContainsAny(kind, "\r\n\x00") {
		return Result{}, fail("PLAN", "事务 kind 无效")
	}
	absolute, err := safeRoot(root, true)
	if err != nil {
		return Result{}, err
	}
	root = absolute
	records, err := normalize(root, ops)
	if err != nil {
		return Result{}, err
	}
	release, err := acquire(root)
	if err != nil {
		return Result{}, err
	}
	defer release()
	boundGuards := map[string]domain.Descriptor{}
	var spellings safefs.PathSet
	for _, r := range records {
		if e := spellings.Add(r.Path); e != nil {
			return Result{}, fail("PLAN", e.Error())
		}
	}
	for ref, want := range guards {
		if _, e := safefs.Path(root, ref); e != nil {
			return Result{}, e
		}
		if !descriptorValid(want) {
			return Result{}, fail("PLAN", "无效只读输入摘要: "+ref)
		}
		if e := spellings.Add(ref); e != nil {
			return Result{}, fail("PLAN", e.Error())
		}
		boundGuards[ref] = normalizedDescriptor(want)
	}
	if err = checkGuards(root, boundGuards, nil); err != nil {
		return Result{}, err
	}
	for _, r := range records {
		if d, ok := boundGuards[r.Path]; ok && !same(d, r.Before) {
			return Result{}, fail("PLAN", "只读输入与写入基线矛盾: "+r.Path)
		}
	}
	all, _, err := scan(root)
	if err != nil {
		return Result{}, err
	}
	for _, l := range all {
		if !terminal(l.journal.Phase) {
			return result(l, "pending"), fail("INTERRUPTED", "存在未完成事务，先 recover")
		}
	}
	for _, r := range records {
		if err := assertCurrent(root, r, r.Before); err != nil {
			return Result{}, err
		}
	}
	if len(records) == 0 {
		return Result{Status: "unchanged"}, nil
	}
	sequence := uint64(1)
	if len(all) > 0 {
		sequence = all[len(all)-1].plan.Sequence + 1
		if sequence == 0 {
			return Result{}, fail("STATE", "事务序号已耗尽")
		}
	}
	id, err := identifier()
	if err != nil {
		return Result{}, err
	}
	staging := stateRef + "/.preparing-" + id
	l := loaded{base: stateRef + "/" + id, plan: plan{SchemaVersion: 2, Sequence: sequence, ID: id, Root: root, Kind: kind, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Operations: records, Guards: boundGuards}}
	planBytes := encode(l.plan)
	l.journal = journal{SchemaVersion: 1, ID: id, PlanDigest: safefs.Digest(planBytes), Phase: "prepared", UpdatedAt: l.plan.CreatedAt}
	if err := durable(root, staging+"/plan.json", planBytes, 0600); err != nil {
		return Result{}, err
	}
	objects := map[string]bool{}
	for i, r := range records {
		if err := ctx.Err(); err != nil {
			return Result{Status: "cancelled"}, err
		}
		if r.Before.Type == "file" {
			b, e := read(root, r.Path)
			if e != nil {
				return Result{}, e
			}
			if safefs.Digest(b) != r.Before.Digest {
				return Result{}, fail("CONCURRENT", "备份期间文件变化: "+r.Path)
			}
			if e := storeObject(root, staging, r.Before, b, objects); e != nil {
				return Result{}, e
			}
		}
		if r.After.Type == "file" {
			if err := storeObject(root, staging, r.After, ops[i].Data, objects); err != nil {
				return Result{}, err
			}
		}
	}
	if len(objects) > 0 {
		p, e := safefs.Path(root, staging+"/objects")
		if e != nil {
			return Result{}, e
		}
		if e = syncDirectory(p); e != nil {
			return Result{}, e
		}
	}
	if err := durable(root, staging+"/intent.wal", nil, 0600); err != nil {
		return Result{}, err
	}
	if err := durable(root, staging+"/journal.json", encode(l.journal), 0600); err != nil {
		return Result{}, err
	}
	from, e := safefs.Path(root, staging)
	if e != nil {
		return Result{}, e
	}
	to, e := safefs.Path(root, l.base)
	if e != nil {
		return Result{}, e
	}
	if err := os.Rename(from, to); err != nil {
		return Result{}, err
	}
	if err := syncDirectory(filepath.Dir(to)); err != nil {
		return result(l, "pending"), err
	}
	appliedErr := apply(ctx, root, &l)
	if appliedErr != nil {
		restoreResult, restoreErr := restore(root, &l, false)
		if restoreErr != nil {
			return result(l, "blocked"), fmt.Errorf("%w; 恢复阻断: %v", appliedErr, restoreErr)
		}
		return restoreResult, appliedErr
	}
	return result(l, "applied"), nil
}

func apply(ctx context.Context, root string, l *loaded) (applyErr error) {
	dirty := map[string]bool{}
	defer func() {
		if err := flushDirectories(dirty); applyErr == nil {
			applyErr = err
		}
	}()
	if err := update(root, l, "applying"); err != nil {
		return err
	}
	if err := checkGuards(root, l.plan.Guards, nil); err != nil {
		return err
	}
	walPath, err := safefs.Path(root, l.base+"/intent.wal")
	if err != nil {
		return err
	}
	wal, err := os.OpenFile(walPath, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer wal.Close()
	// All inputs are checked before the first write, then checked again per operation.
	for _, r := range l.plan.Operations {
		if err := assertCurrent(root, r, r.Before); err != nil {
			return err
		}
	}
	// Persist the complete intention set before any target mutation. Recovery
	// accepts either before or after bytes; operations not yet begun remain before.
	for i := range l.plan.Operations {
		if _, err := wal.Write(encode(intent{SchemaVersion: 1, Index: i})); err != nil {
			return err
		}
	}
	if err := wal.Sync(); err != nil {
		return err
	}
	for i, r := range l.plan.Operations {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := assertCurrent(root, r, r.Before); err != nil {
			return err
		}
		if err := replace(root, l.base, i, r, r.After, "apply", dirty); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, r := range l.plan.Operations {
		if err := assertCurrent(root, r, r.After); err != nil {
			return err
		}
	}
	if err := flushDirectories(dirty); err != nil {
		return err
	}
	written := map[string]bool{}
	for _, r := range l.plan.Operations {
		written[r.Path] = true
	}
	if err := checkGuards(root, l.plan.Guards, written); err != nil {
		return err
	}
	return update(root, l, "committed")
}
func checkGuards(root string, guards map[string]domain.Descriptor, skip map[string]bool) error {
	keys := []string{}
	for ref := range guards {
		keys = append(keys, ref)
	}
	sort.Strings(keys)
	for _, ref := range keys {
		if skip[ref] {
			continue
		}
		got, e := safefs.Describe(root, ref)
		if e != nil {
			return e
		}
		if !same(got, guards[ref]) {
			return fail("INPUT_DRIFT", "只读事实输入变化: "+ref)
		}
	}
	return nil
}

func flushDirectories(dirty map[string]bool) error {
	paths := make([]string, 0, len(dirty))
	for p := range dirty {
		paths = append(paths, p)
	}
	sort.Slice(paths, func(i, j int) bool { return len(paths[i]) > len(paths[j]) })
	for _, p := range paths {
		if err := syncDirectory(p); err != nil {
			return err
		}
		delete(dirty, p)
	}
	return nil
}
func persistDirectory(p string, dirty map[string]bool) error {
	if dirty == nil {
		return syncDirectory(p)
	}
	dirty[p] = true
	return nil
}

func attempted(root string, l loaded) (int, error) {
	b, err := read(root, l.base+"/intent.wal")
	if err != nil {
		return 0, err
	}
	end := bytes.LastIndexByte(b, '\n')
	if end < 0 {
		return 0, nil
	}
	count := 0
	for _, line := range bytes.Split(b[:end], []byte{'\n'}) {
		var v intent
		if json.Unmarshal(line, &v) != nil || v.SchemaVersion != 1 || v.Index != count || count >= len(l.plan.Operations) {
			return 0, fail("STATE", "事务 WAL 顺序或内容损坏")
		}
		count++
	}
	if l.journal.Phase == "committed" && count != len(l.plan.Operations) {
		return 0, fail("STATE", "成功事务 WAL 不完整")
	}
	return count, nil
}

func storeObject(root, base string, d domain.Descriptor, b []byte, objects map[string]bool) error {
	if safefs.Digest(b) != d.Digest {
		return fail("CONCURRENT", "候选或归档字节与计划摘要不一致")
	}
	if objects[d.Digest] {
		return nil
	}
	ref := base + "/objects/" + d.Digest
	if err := ensureParents(root, ref); err != nil {
		return err
	}
	p, err := safefs.Path(root, ref)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	objects[d.Digest] = true
	return nil
}

func blob(root, base string, i int, d domain.Descriptor, original bool) ([]byte, error) {
	if d.Type == "missing" {
		return nil, nil
	}
	// Content is immutable and shared by digest; each original mode remains in
	// the plan descriptor. Repeated Skill projections need only one durable blob.
	b, err := read(root, base+"/objects/"+d.Digest)
	if err != nil {
		return nil, err
	}
	if safefs.Digest(b) != d.Digest {
		return nil, fail("STATE", "事务内容归档摘要不匹配")
	}
	return b, nil
}
func temporary(ref, base string, i int, role string) string {
	id := filepath.Base(base)
	return fmt.Sprintf("%s.yss-txn-%s-%d-%s.tmp", ref, id, i, role)
}
func tempCheck(root, base string, i int, r record, role string, d domain.Descriptor) error {
	ref := temporary(r.Path, base, i, role)
	actual, err := safefs.Describe(root, ref)
	if err != nil {
		return err
	}
	if actual.Type == "missing" {
		return nil
	}
	if d.Type != "file" || !same(actual, d) {
		return fail("RECOVERY_FAILED", "事务临时文件含未知修改，保留: "+ref)
	}
	return nil
}
func replace(root, base string, i int, r record, wanted domain.Descriptor, role string, dirty map[string]bool) error {
	if err := guard(root, r.Path); err != nil {
		return err
	}
	p, err := safefs.Path(root, r.Path)
	if err != nil {
		return err
	}
	if wanted.Type == "missing" {
		if err := os.Remove(p); err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		return persistDirectory(filepath.Dir(p), dirty)
	}
	original := role == "restore"
	b, err := blob(root, base, i, wanted, original)
	if err != nil {
		return err
	}
	if err := ensureParents(root, r.Path); err != nil {
		return err
	}
	tempRef := temporary(r.Path, base, i, role)
	temp, err := safefs.Path(root, tempRef)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err = f.Write(b); err == nil {
		err = f.Chmod(os.FileMode(wanted.Mode))
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if _, err = safefs.Path(root, r.Path); err != nil {
		return err
	}
	if err = os.Rename(temp, p); err != nil {
		return err
	}
	return persistDirectory(filepath.Dir(p), dirty)
}

func restore(root string, l *loaded, rollback bool) (restoreResult Result, restoreErr error) {
	dirty := map[string]bool{}
	defer func() {
		if err := flushDirectories(dirty); restoreErr == nil {
			restoreErr = err
		}
	}()
	n, err := attempted(root, *l)
	if err != nil {
		return result(*l, "blocked"), err
	}
	// Complete preflight prevents a later conflict from partially undoing earlier files.
	for i := 0; i < n; i++ {
		r := l.plan.Operations[i]
		if err := guard(root, r.Path); err != nil {
			return result(*l, "blocked"), err
		}
		current, err := safefs.Describe(root, r.Path)
		if err != nil {
			return result(*l, "blocked"), err
		}
		if !same(current, r.Before) && !same(current, r.After) {
			return result(*l, "blocked"), fail("RECOVERY_FAILED", "文件有后续修改，事务整体停止: "+r.Path)
		}
		if _, err := blob(root, l.base, i, r.Before, true); err != nil {
			return result(*l, "blocked"), err
		}
		if err := tempCheck(root, l.base, i, r, "apply", r.After); err != nil {
			return result(*l, "blocked"), err
		}
		if err := tempCheck(root, l.base, i, r, "restore", r.Before); err != nil {
			return result(*l, "blocked"), err
		}
	}
	phase := "recovering"
	if rollback {
		phase = "rolling-back"
	}
	if err := update(root, l, phase); err != nil {
		return result(*l, "blocked"), err
	}
	for i := n - 1; i >= 0; i-- {
		r := l.plan.Operations[i]
		for _, role := range []string{"apply", "restore"} {
			ref := temporary(r.Path, l.base, i, role)
			p, e := safefs.Path(root, ref)
			if e != nil {
				return result(*l, "blocked"), e
			}
			if e = os.Remove(p); e != nil && !os.IsNotExist(e) {
				return result(*l, "blocked"), e
			}
			if e == nil {
				if e = syncDirectory(filepath.Dir(p)); e != nil {
					return result(*l, "blocked"), e
				}
			}
		}
		current, e := safefs.Describe(root, r.Path)
		if e != nil {
			return result(*l, "blocked"), e
		}
		if same(current, r.Before) {
			continue
		}
		if !same(current, r.After) {
			return result(*l, "blocked"), fail("RECOVERY_FAILED", "恢复期间发生用户修改: "+r.Path)
		}
		if e := replace(root, l.base, i, r, r.Before, "restore", dirty); e != nil {
			return result(*l, "blocked"), e
		}
	}
	for i := 0; i < n; i++ {
		r := l.plan.Operations[i]
		if err := assertCurrent(root, r, r.Before); err != nil {
			return result(*l, "blocked"), err
		}
	}
	if err := flushDirectories(dirty); err != nil {
		return result(*l, "blocked"), err
	}
	final, status := "recovered", "recovered"
	if rollback {
		final, status = "rolled-back", "rolled-back"
	}
	if err := update(root, l, final); err != nil {
		return result(*l, "blocked"), err
	}
	return result(*l, status), nil
}

func Recover(root string) (Result, error) {
	root, err := safeRoot(root, false)
	if err != nil {
		return Result{}, err
	}
	all, _, err := scan(root)
	if err != nil {
		return Result{}, err
	}
	pending := []loaded{}
	for _, l := range all {
		if !terminal(l.journal.Phase) {
			pending = append(pending, l)
		}
	}
	if len(pending) == 0 {
		return Result{Status: "unchanged"}, nil
	}
	if len(pending) != 1 {
		return Result{}, fail("STATE", "多个未完成事务，不能猜测恢复顺序")
	}
	release, err := acquire(root)
	if err != nil {
		return Result{}, err
	}
	defer release()
	l, err := load(root, pending[0].plan.ID)
	if err != nil {
		return Result{}, err
	}
	if terminal(l.journal.Phase) {
		return result(l, "unchanged"), nil
	}
	return restore(root, &l, l.journal.Phase == "rolling-back")
}

func Rollback(root string) (Result, error) {
	return rollbackWithValidator(context.Background(), root, "", nil)
}

// RollbackKind applies only to the latest committed (or already rolled-back)
// transaction of the requested kind. It never searches past a later success.
func RollbackKind(root, wantKind string) (Result, error) {
	if strings.TrimSpace(wantKind) == "" || strings.ContainsAny(wantKind, "\r\n\x00") {
		return Result{}, fail("KIND", "回退必须指定非空事务 kind")
	}
	return rollbackWithValidator(context.Background(), root, wantKind, nil)
}

func rollbackWithValidator(ctx context.Context, root, wantKind string, validate ScopeValidator) (Result, error) {
	if ctx == nil {
		return Result{}, fail("ARGUMENT", "回退必须提供 context")
	}
	if err := ctx.Err(); err != nil {
		return Result{Status: "cancelled"}, domain.Wrap("CANCELLED", err)
	}
	root, err := safeRoot(root, false)
	if err != nil {
		return Result{}, err
	}
	all, _, err := scan(root)
	if err != nil {
		return Result{}, err
	}
	if len(all) == 0 {
		if wantKind != "" {
			return Result{}, fail("KIND", "没有可匹配的成功事务: "+wantKind)
		}
		return Result{Status: "unchanged"}, nil
	}
	if err = ctx.Err(); err != nil {
		return Result{Status: "cancelled"}, domain.Wrap("CANCELLED", err)
	}
	release, err := acquire(root)
	if err != nil {
		return Result{}, err
	}
	defer release()
	if err = ctx.Err(); err != nil {
		return Result{Status: "cancelled"}, domain.Wrap("CANCELLED", err)
	}
	all, _, err = scan(root)
	if err != nil {
		return Result{}, err
	}
	for _, l := range all {
		if !terminal(l.journal.Phase) {
			return result(l, "pending"), fail("INTERRUPTED", "先 recover 未完成事务")
		}
	}
	// Only the most recent committed application is eligible, never an older one
	// after its successor has already been rolled back.
	var latest *loaded
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].journal.Phase == "committed" || all[i].journal.Phase == "rolled-back" {
			latest = &all[i]
			break
		}
	}
	if wantKind != "" && (latest == nil || latest.plan.Kind != wantKind) {
		actual := "无成功事务"
		if latest != nil {
			actual = latest.plan.Kind
		}
		return Result{Status: "blocked"}, fail("KIND", "最新成功事务 kind="+actual+"，不能作为 "+wantKind+" 回退")
	}
	if latest != nil {
		if err = validateArchiveScope(*latest, validate); err != nil {
			return result(*latest, "blocked"), err
		}
	}
	if err = ctx.Err(); err != nil {
		return Result{Status: "cancelled"}, domain.Wrap("CANCELLED", err)
	}
	if latest == nil || latest.journal.Phase == "rolled-back" {
		return Result{Status: "unchanged"}, nil
	}
	for _, r := range latest.plan.Operations {
		if err := assertCurrent(root, r, r.After); err != nil {
			return result(*latest, "blocked"), err
		}
	}
	if err = ctx.Err(); err != nil {
		return Result{Status: "cancelled"}, domain.Wrap("CANCELLED", err)
	}
	return restore(root, latest, true)
}

// ArchivedFile reads candidate bytes from a verified immutable transaction
// archive. A unique pending transaction takes precedence over the latest
// committed transaction. It creates no state and never reads target file bytes.
func ArchivedFile(root, ref string) ([]byte, error) {
	root, err := safeRoot(root, false)
	if err != nil {
		return nil, err
	}
	if err := safefs.ValidateRef(ref); err != nil {
		return nil, err
	}
	all, _, err := scan(root)
	if err != nil {
		return nil, err
	}
	pending := []loaded{}
	for _, l := range all {
		if !terminal(l.journal.Phase) {
			pending = append(pending, l)
		}
	}
	if len(pending) > 1 {
		return nil, fail("AMBIGUOUS", "多个未完成事务，不能猜测候选归档")
	}
	var selected *loaded
	if len(pending) == 1 {
		selected = &pending[0]
	} else {
		for i := len(all) - 1; i >= 0; i-- {
			if all[i].journal.Phase == "committed" {
				selected = &all[i]
				break
			}
		}
	}
	if selected == nil {
		return nil, fail("ARCHIVE", "没有可读取的候选事务归档")
	}
	for i, r := range selected.plan.Operations {
		if r.Path != ref {
			continue
		}
		if r.After.Type != "file" {
			return nil, fail("ARCHIVE", "归档路径没有候选文件: "+ref)
		}
		return blob(root, selected.base, i, r.After, false)
	}
	return nil, fail("ARCHIVE", "事务没有登记候选路径: "+ref)
}

// Status reads journals only; it creates no directory and acquires no write lock.
func Status(root string) (Result, error) {
	root, err := safeRoot(root, false)
	if err != nil {
		return Result{}, err
	}
	all, preparations, err := scan(root)
	if err != nil {
		return Result{}, err
	}
	out := Result{Status: "ok", Preparations: preparations, Transactions: []Summary{}}
	for _, l := range all {
		out.Transactions = append(out.Transactions, Summary{TransactionID: l.plan.ID, Kind: l.plan.Kind, Phase: l.journal.Phase, PlanDigest: l.journal.PlanDigest, Operations: len(l.plan.Operations), CreatedAt: l.plan.CreatedAt, Sequence: l.plan.Sequence})
		if !terminal(l.journal.Phase) {
			out.Pending = append(out.Pending, l.plan.ID)
		}
	}
	if len(out.Pending) > 0 {
		out.Status = "pending"
	}
	return out, nil
}
