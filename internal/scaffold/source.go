package scaffold

import (
	"encoding/base64"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/bundle"
)

// Source provides the template-repository files a generator consumes. Paths are
// slash-separated and relative to the template root.
type Source interface {
	// Read returns a file's exact bytes.
	Read(path string) ([]byte, error)
	// Mode returns the recorded permission bits of a file.
	Mode(path string) (os.FileMode, bool)
	// Has reports whether the file exists.
	Has(path string) bool
	// Walk lists every file below prefix (a file or directory) as paths relative to
	// prefix, in bytewise order. A file prefix lists itself as ".".
	Walk(prefix string) []string
}

type bundleSource struct {
	files map[string]bundle.File
}

// BundleSource reads from the embedded fixed Spec Bundle.
func BundleSource() (Source, error) {
	loaded, err := bundle.Load("spec")
	if err != nil {
		return nil, err
	}
	return newBundleSource(loaded.Files), nil
}

func newBundleSource(files map[string]bundle.File) *bundleSource {
	return &bundleSource{files: files}
}

func parent(path string) string {
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[:i]
	}
	return ""
}

func (s *bundleSource) Read(path string) ([]byte, error) {
	file, ok := s.files[path]
	if !ok {
		return nil, fmt.Errorf("内置 Bundle 缺少文件: %s", path)
	}
	return base64.StdEncoding.DecodeString(file.Data)
}

func (s *bundleSource) Mode(path string) (os.FileMode, bool) {
	file, ok := s.files[path]
	return os.FileMode(file.Mode), ok
}

func (s *bundleSource) Has(path string) bool {
	_, ok := s.files[path]
	return ok
}

func (s *bundleSource) Walk(prefix string) []string {
	if _, ok := s.files[prefix]; ok {
		return []string{"."}
	}
	var out []string
	for path := range s.files {
		if strings.HasPrefix(path, prefix+"/") {
			out = append(out, strings.TrimPrefix(path, prefix+"/"))
		}
	}
	sort.Strings(out)
	return out
}
