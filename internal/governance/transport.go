package governance

import (
	"archive/zip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"golang.org/x/text/cases"
)

const maxArchiveBytes uint64 = 512 * 1024 * 1024
const maxArchiveFiles = 20000

func inside(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func externalPath(root, p string) (string, error) {
	if !filepath.IsAbs(p) {
		return "", domain.Fail("PATH", "输出或运行存储必须使用仓外绝对路径")
	}
	p = filepath.Clean(p)
	if inside(root, p) {
		return "", domain.Fail("PATH", "输出或运行存储不得进入项目根目录")
	}
	protected, err := protectedRoots(root)
	if err != nil {
		return "", err
	}
	for _, registered := range protected {
		if inside(registered, p) {
			return "", domain.Fail("PATH", "输出或运行存储不得进入已登记仓库: "+registered)
		}
	}
	current := p
	for {
		st, err := os.Lstat(current)
		if err == nil {
			if st.Mode()&os.ModeSymlink != 0 {
				return "", domain.Fail("PATH", "拒绝符号链接路径: "+current)
			}
			if current != p && !st.IsDir() {
				return "", domain.Fail("PATH", "路径父级不是目录")
			}
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return p, nil
}

func zipName(name string) error {
	if err := safefs.ValidateRef(name); err != nil {
		return domain.Wrap("ZIP", err)
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return domain.Fail("ZIP", "ZIP 路径含控制字符")
		}
	}
	return nil
}

func zipPrefixes(ref string, fold cases.Caser, prefixes map[string]string) error {
	parts := strings.Split(ref, "/")
	for i := 1; i <= len(parts); i++ {
		prefix := strings.Join(parts[:i], "/")
		key := fold.String(prefix)
		if old, ok := prefixes[key]; ok && old != prefix {
			return domain.Fail("ZIP", "ZIP 前缀存在 Unicode 折叠碰撞: "+old+" / "+prefix)
		}
		prefixes[key] = prefix
	}
	return nil
}

func zipEntries(files []*zip.File) (uint64, error) {
	if len(files) > maxArchiveFiles {
		return 0, domain.Fail("ZIP_LIMIT", "ZIP 文件数超限")
	}
	seen := map[string]bool{}
	var spellings safefs.PathSet
	foldedPrefixes := map[string]string{}
	var size uint64
	fold := cases.Fold()
	for _, f := range files {
		if err := zipName(f.Name); err != nil {
			return 0, err
		}
		if err := spellings.Add(f.Name); err != nil {
			return 0, domain.Wrap("ZIP", err)
		}
		key := fold.String(f.Name)
		if err := zipPrefixes(f.Name, fold, foldedPrefixes); err != nil {
			return 0, err
		}
		if seen[key] {
			return 0, domain.Fail("ZIP", "ZIP 条目重复或大小写折叠冲突: "+f.Name)
		}
		seen[key] = true
		if !f.Mode().IsRegular() || f.Flags&1 != 0 {
			return 0, domain.Fail("ZIP", "ZIP 只允许未加密普通文件: "+f.Name)
		}
		if f.UncompressedSize64 > maxArchiveBytes-size {
			return 0, domain.Fail("ZIP_LIMIT", "ZIP 展开大小超限")
		}
		size += f.UncompressedSize64
	}
	for key := range seen {
		parts := strings.Split(key, "/")
		for i := 1; i < len(parts); i++ {
			if seen[strings.Join(parts[:i], "/")] {
				return 0, domain.Fail("ZIP", "ZIP 文件与目录路径冲突")
			}
		}
	}
	return size, nil
}

func archiveRun(ctx context.Context, action, root string, args map[string]string) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if action != "pack" && action != "unpack" && action != "verify" {
		return nil, domain.Fail("UNPORTED", "ZIP 动作尚未迁移: "+action)
	}
	source := args["source"]
	if source == "" {
		source = args["file"]
	}
	if source == "" {
		source = args["arg0"]
	}
	sourcePath, err := safefs.Path(root, source)
	if err != nil {
		return nil, err
	}
	switch action {
	case "verify", "unpack":
		r, err := zip.OpenReader(sourcePath)
		if err != nil {
			return nil, domain.Wrap("ZIP", err)
		}
		defer r.Close()
		total, err := zipEntries(r.File)
		if err != nil {
			return nil, err
		}
		var dir *os.Root
		var output string
		if action == "unpack" {
			output, err = externalPath(root, args["output"])
			if err != nil {
				return nil, err
			}
			if err = ctx.Err(); err != nil {
				return nil, err
			}
			if err = ctx.Err(); err != nil {
				return nil, err
			}
			if err = os.MkdirAll(filepath.Dir(output), 0700); err != nil {
				return nil, err
			}
			if err = os.Mkdir(output, 0700); err != nil {
				return nil, err
			}
			dir, err = os.OpenRoot(output)
			if err != nil {
				_ = os.Remove(output)
				return nil, err
			}
			defer dir.Close()
			complete := false
			defer func() {
				if !complete {
					_ = os.RemoveAll(output)
				}
			}()
			for _, f := range r.File {
				if err = extractZipFile(ctx, f, dir); err != nil {
					return nil, err
				}
			}
			if err = ctx.Err(); err != nil {
				return nil, err
			}
			complete = true
		} else {
			for _, f := range r.File {
				if err = extractZipFile(ctx, f, nil); err != nil {
					return nil, err
				}
			}
		}
		return map[string]any{"format": "zip", "files": len(r.File), "bytes": total, "output": output, "scope": "transport-only", "execution_authorization": "not-evaluated"}, nil
	case "pack":
		st, err := os.Lstat(sourcePath)
		if err != nil {
			return nil, err
		}
		if !st.IsDir() {
			return nil, domain.Fail("ZIP", "pack 来源必须是目录")
		}
		output, err := externalPath(root, args["output"])
		if err != nil {
			return nil, err
		}
		refs := []string{}
		var total uint64
		seen := map[string]bool{}
		var spellings safefs.PathSet
		foldedPrefixes := map[string]string{}
		fold := cases.Fold()
		err = filepath.WalkDir(sourcePath, func(p string, d os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if e = ctx.Err(); e != nil {
				return e
			}
			if p == sourcePath {
				return nil
			}
			if d.Type()&os.ModeSymlink != 0 {
				return domain.Fail("ZIP", "禁止符号链接: "+p)
			}
			if d.IsDir() {
				return nil
			}
			rel, e := filepath.Rel(sourcePath, p)
			if e != nil {
				return e
			}
			rel = filepath.ToSlash(rel)
			if e = zipName(rel); e != nil {
				return e
			}
			if e = spellings.Add(rel); e != nil {
				return domain.Wrap("ZIP", e)
			}
			if e = zipPrefixes(rel, fold, foldedPrefixes); e != nil {
				return e
			}
			if seen[fold.String(rel)] {
				return domain.Fail("ZIP", "来源文件大小写冲突")
			}
			seen[fold.String(rel)] = true
			info, e := d.Info()
			if e != nil {
				return e
			}
			if !info.Mode().IsRegular() {
				return domain.Fail("ZIP", "来源只允许普通文件")
			}
			if info.Size() < 0 || uint64(info.Size()) > maxArchiveBytes-total {
				return domain.Fail("ZIP_LIMIT", "来源大小超限")
			}
			total += uint64(info.Size())
			refs = append(refs, rel)
			if len(refs) > maxArchiveFiles {
				return domain.Fail("ZIP_LIMIT", "来源文件数超限")
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		if err = os.MkdirAll(filepath.Dir(output), 0700); err != nil {
			return nil, err
		}
		file, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return nil, err
		}
		complete := false
		defer func() {
			_ = file.Close()
			if !complete {
				_ = os.Remove(output)
			}
		}()
		w := zip.NewWriter(file)
		var actual uint64
		for _, ref := range refs {
			if err = ctx.Err(); err != nil {
				return nil, err
			}
			p, e := safefs.Path(sourcePath, ref)
			if e != nil {
				return nil, e
			}
			input, e := os.Open(p)
			if e != nil {
				return nil, e
			}
			info, e := input.Stat()
			if e != nil || !info.Mode().IsRegular() {
				_ = input.Close()
				return nil, domain.Fail("ZIP", "来源文件发生类型漂移")
			}
			h := zip.FileHeader{Name: ref, Method: zip.Deflate}
			h.SetModTime(time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC))
			h.SetMode(0644)
			entry, e := w.CreateHeader(&h)
			if e != nil {
				_ = input.Close()
				return nil, e
			}
			n, e := io.Copy(entry, io.LimitReader(cancellationReader{ctx, input}, int64(maxArchiveBytes-actual)+1))
			_ = input.Close()
			if e != nil {
				return nil, e
			}
			actual += uint64(n)
			if actual > maxArchiveBytes {
				return nil, domain.Fail("ZIP_LIMIT", "来源内容发生大小漂移")
			}
		}
		if err = w.Close(); err != nil {
			return nil, err
		}
		if err = file.Sync(); err != nil {
			return nil, err
		}
		if err = file.Close(); err != nil {
			return nil, err
		}
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		complete = true
		return map[string]any{"format": "zip", "files": len(refs), "bytes": actual, "output": output, "scope": "transport-only", "execution_authorization": "not-evaluated"}, nil
	default:
		return nil, domain.Fail("UNPORTED", "ZIP 动作尚未迁移: "+action)
	}
}

func extractZipFile(ctx context.Context, f *zip.File, root *os.Root) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	input, err := f.Open()
	if err != nil {
		return domain.Wrap("ZIP", err)
	}
	defer input.Close()
	var out io.Writer = io.Discard
	var file *os.File
	if root != nil {
		parent := filepath.Dir(f.Name)
		if parent != "." {
			if err = root.MkdirAll(parent, 0700); err != nil {
				return err
			}
		}
		file, err = root.OpenFile(f.Name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		defer file.Close()
		out = file
	}
	n, err := io.Copy(out, io.LimitReader(cancellationReader{ctx, input}, int64(f.UncompressedSize64)+1))
	if err != nil {
		return domain.Wrap("ZIP", err)
	}
	if uint64(n) != f.UncompressedSize64 {
		return domain.Fail("ZIP", "ZIP 展开大小与清单不一致")
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if file != nil {
		return file.Sync()
	}
	return nil
}

// A regular-file copy observes cancellation at each chunk, including archives
// that fit into one CopyBuffer read. The caller also checks before completion.
type cancellationReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r cancellationReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.reader.Read(b)
	if cancelled := r.ctx.Err(); cancelled != nil {
		return n, cancelled
	}
	return n, err
}
