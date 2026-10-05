package governance

import (
	"archive/zip"
	"bytes"
	"fmt"
	"golang.org/x/text/cases"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

type semanticArchiveFile struct {
	Data []byte
	Mode uint32
}

func (v *view) describe(ref string) (domain.Descriptor, error) {
	if v.virtual == nil {
		return safefs.Describe(v.root, ref)
	}
	if err := safefs.ValidateRef(ref); err != nil {
		return domain.Descriptor{}, err
	}
	if file, ok := v.virtual[ref]; ok {
		return domain.Descriptor{Type: "file", Digest: safefs.Digest(file.Data), Mode: domain.FileMode(file.Mode)}, nil
	}
	for file := range v.virtual {
		if strings.HasPrefix(file, ref+"/") {
			return domain.Descriptor{Type: "directory", Mode: 0755}, nil
		}
	}
	return domain.Descriptor{Type: "missing"}, nil
}

// zipSourceSession reads a bounded immutable ZIP without extracting, invoking
// interpreters, or granting the archive access to the physical project root.
func (s *semanticSession) zipSourceSession(ref string) (*semanticSession, string, error) {
	raw, err := s.bytes(ref)
	if err != nil {
		return nil, "", err
	}
	if len(raw) > 512<<20 {
		return nil, "", s.reject("HANDOFF_LIMIT", "ZIP 压缩文件超限")
	}
	reader, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, "", s.reject("HANDOFF_ZIP", err.Error())
	}
	if len(reader.File) == 0 || len(reader.File) > 20000 {
		return nil, "", s.reject("HANDOFF_LIMIT", "ZIP 成员数量非法")
	}
	files := map[string]semanticArchiveFile{}
	names := map[string]bool{}
	canonical := map[string]string{}
	fileNames := map[string]bool{}
	fold := cases.Fold()
	dirs := map[string]bool{}
	var total uint64
	for _, entry := range reader.File {
		if err = s.guard(); err != nil {
			return nil, "", err
		}
		if entry.Flags&1 != 0 {
			return nil, "", s.reject("HANDOFF_ZIP", "ZIP 加密成员不受支持")
		}
		name := entry.Name
		directory := strings.HasSuffix(name, "/")
		if directory {
			name = strings.TrimSuffix(name, "/")
		}
		if !utf8.ValidString(name) || entry.NonUTF8 {
			return nil, "", s.reject("HANDOFF_PATH", "ZIP 文件名编码不明确")
		}
		if err = safefs.ValidateRef(name); err != nil {
			return nil, "", s.reject("HANDOFF_PATH", err.Error())
		}
		folded := fold.String(name)
		if names[folded] {
			return nil, "", s.reject("HANDOFF_PATH", "ZIP 路径重复或大小写冲突")
		}
		names[folded] = true
		for component := name; component != "."; component = path.Dir(component) {
			key := fold.String(component)
			if old, ok := canonical[key]; ok && old != component {
				return nil, "", s.reject("HANDOFF_PATH", "ZIP 父目录大小写或 Unicode 折叠冲突")
			}
			canonical[key] = component
			if len(canonical) > 20000 {
				return nil, "", s.reject("HANDOFF_LIMIT", "ZIP 文件及父目录总数超限")
			}
		}
		if !directory {
			fileNames[folded] = true
		}
		mode := entry.Mode()
		if mode&os.ModeSymlink != 0 || mode.Type() != 0 && !mode.IsDir() {
			return nil, "", s.reject("HANDOFF_PATH", "ZIP 含链接或特殊文件")
		}
		if directory {
			if entry.UncompressedSize64 != 0 {
				return nil, "", s.reject("HANDOFF_ZIP", "ZIP 目录不能携带内容")
			}
			dirs[name] = true
			continue
		}
		if mode.IsDir() {
			return nil, "", s.reject("HANDOFF_PATH", "ZIP 目录标记与路径矛盾")
		}
		if entry.UncompressedSize64 > 512<<20 || total+entry.UncompressedSize64 > 512<<20 {
			return nil, "", s.reject("HANDOFF_LIMIT", "ZIP 展开大小超限")
		}
		rc, err := entry.Open()
		if err != nil {
			return nil, "", s.reject("HANDOFF_ZIP", err.Error())
		}
		var out bytes.Buffer
		chunk := make([]byte, 64<<10)
		for {
			if err = s.guard(); err != nil {
				_ = rc.Close()
				return nil, "", err
			}
			n, e := rc.Read(chunk)
			if n > 0 {
				if uint64(out.Len()+n) > entry.UncompressedSize64 {
					_ = rc.Close()
					return nil, "", s.reject("HANDOFF_LIMIT", "ZIP 实际内容超过声明大小")
				}
				out.Write(chunk[:n])
			}
			if e == io.EOF {
				break
			}
			if e != nil {
				_ = rc.Close()
				return nil, "", s.reject("HANDOFF_ZIP", e.Error())
			}
		}
		if err = rc.Close(); err != nil {
			return nil, "", s.reject("HANDOFF_ZIP", err.Error())
		}
		if uint64(out.Len()) != entry.UncompressedSize64 {
			return nil, "", s.reject("HANDOFF_ZIP", "ZIP 大小与声明不一致")
		}
		total += entry.UncompressedSize64
		files["bundle/"+name] = semanticArchiveFile{Data: out.Bytes(), Mode: uint32(mode.Perm())}
	}
	// File/directory prefix collisions and empty extra directories are invalid.
	for name := range files {
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			if fileNames[fold.String(strings.TrimPrefix(parent, "bundle/"))] {
				return nil, "", s.reject("HANDOFF_PATH", "ZIP 文件被用作父目录")
			}
		}
	}
	for directory := range dirs {
		found := false
		for file := range files {
			if strings.HasPrefix(file, "bundle/"+directory+"/") {
				found = true
				break
			}
		}
		if !found {
			return nil, "", s.reject("HANDOFF_MANIFEST", "ZIP 包含未登记的空目录")
		}
	}
	child := newSemanticSession(s.ctx, filepath.Join(s.root, ".yss-archive-view", safefs.Digest(raw)), s.args)
	child.v.virtual = files
	child.ruleSession = s
	s.children = append(s.children, child)
	child.report.Applicability = append(child.report.Applicability, map[string]any{"id": "zip-source", "source_ref": ref, "read_only": true, "extracted": false})
	s.report.Applicability = append(s.report.Applicability, map[string]any{"id": "zip-source", "source_ref": ref, "read_only": true, "extracted": false})
	return child, "bundle", nil
}

func (s *semanticSession) scanVirtual(ref string, observe bool) ([]string, error) {
	if err := safefs.ValidateRef(ref); err != nil {
		return nil, s.unavailable("PATH", err.Error())
	}
	entries := []string{}
	dirs := map[string]bool{}
	for name := range s.v.virtual {
		if err := s.guard(); err != nil {
			return nil, err
		}
		if name != ref && !strings.HasPrefix(name, ref+"/") {
			continue
		}
		entries = append(entries, "file:"+name)
		if observe {
			if _, err := s.v.watch(name); err != nil {
				return nil, err
			}
		}
		for dir := path.Dir(name); dir != "." && (dir == ref || strings.HasPrefix(dir, ref+"/")); dir = path.Dir(dir) {
			dirs[dir] = true
		}
	}
	if len(entries) == 0 {
		if observe {
			if _, err := s.v.watch(ref); err != nil {
				return nil, err
			}
		}
		return []string{"missing:" + ref}, nil
	}
	for dir := range dirs {
		entries = append(entries, fmt.Sprintf("dir:%s:%o", dir, 0755))
	}
	sort.Strings(entries)
	return entries, nil
}
