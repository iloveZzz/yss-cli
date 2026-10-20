package governance

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

const runtimeInputLimit = 16 << 20

type runtimeObject struct {
	digest, relative  string
	bytes, compressed int64
}

// storeRuntimeInput 与 Node 的共享对象布局一致：objects/<前两位>/<sha256>.gz，摘要基于原字节。
// 已存在的对象（含 Node 写入的）只复核解压后摘要，不覆盖；运行存储内部不允许符号链接。
func storeRuntimeInput(dir string, data []byte) (*runtimeObject, error) {
	if len(data) > runtimeInputLimit {
		return nil, domain.Fail("INPUT", "运行输入超过 16 MiB")
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	relative := "objects/" + digest[:2] + "/" + digest + ".gz"
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	for _, sub := range []string{"objects", "objects/" + digest[:2]} {
		path := filepath.Join(dir, filepath.FromSlash(sub))
		st, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			if err = os.Mkdir(path, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
				return nil, err
			}
			if err = safefs.SyncDirectory(filepath.Dir(path)); err != nil {
				return nil, err
			}
			if st, err = os.Lstat(path); err != nil {
				return nil, err
			}
		} else if err != nil {
			return nil, err
		}
		if st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
			return nil, domain.Fail("RUNTIME", "运行存储内部路径必须是普通目录: "+sub)
		}
	}
	file := filepath.Join(dir, filepath.FromSlash(relative))
	if st, err := os.Lstat(file); errors.Is(err, fs.ErrNotExist) {
		var buf bytes.Buffer
		w := gzip.NewWriter(&buf)
		if _, err = w.Write(data); err != nil {
			return nil, err
		}
		if err = w.Close(); err != nil {
			return nil, err
		}
		if err := verifyRuntimeObject(bytes.NewReader(buf.Bytes()), digest, relative); err != nil {
			return nil, err
		}
		if _, err = safefs.PublishFile(dir, relative, bytes.NewReader(buf.Bytes())); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	} else if !st.Mode().IsRegular() {
		return nil, domain.Fail("RUNTIME", "运行输入对象必须是普通文件")
	}
	file, err := safefs.Path(dir, relative)
	if err != nil {
		return nil, err
	}
	before, err := os.Lstat(file)
	if err != nil || !before.Mode().IsRegular() {
		return nil, domain.Fail("RUNTIME", "运行输入对象必须是普通文件")
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || !os.SameFile(before, st) {
		return nil, domain.Fail("RUNTIME", "运行输入对象读取期间身份变化")
	}
	if err = verifyRuntimeObject(f, digest, relative); err != nil {
		return nil, err
	}
	return &runtimeObject{digest: digest, relative: relative, bytes: int64(len(data)), compressed: st.Size()}, nil
}

func verifyRuntimeObject(input io.Reader, digest, relative string) error {
	corrupt := func() error {
		return domain.Explain(domain.Fail("RUNTIME_INTEGRITY", "运行输入对象损坏: "+relative), "RUNTIME_OBJECT_CORRUPT", "保留现有对象与引用证据；先检查运行存储，不自动覆盖损坏对象。", map[string]any{"object": relative, "digest": digest})
	}
	r, err := gzip.NewReader(input)
	if err != nil {
		return corrupt()
	}
	defer r.Close()
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(r, runtimeInputLimit+1))
	if err != nil || n > runtimeInputLimit || hex.EncodeToString(h.Sum(nil)) != digest {
		return corrupt()
	}
	return nil
}
