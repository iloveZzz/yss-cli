package scaffold

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Error is a generator rejection; Message keeps the Node generator's wording.
type Error struct{ Message string }

func (e *Error) Error() string { return e.Message }

func fail(message string) error { return &Error{Message: message} }

func failf(format string, args ...any) error { return &Error{Message: fmt.Sprintf(format, args...)} }

const (
	defaultFileMode os.FileMode = 0o644
	executableMode  os.FileMode = 0o755
)

type treeFile struct {
	data  []byte
	mode  os.FileMode // 0 keeps the default permissions (0666 filtered by umask)
	fixed bool
}

// Tree is the generated project held in memory until it is committed. Paths are
// slash-separated and relative to the tree root.
type Tree struct {
	files map[string]*treeFile
	dirs  map[string]os.FileMode
}

func newTree() *Tree {
	return &Tree{files: map[string]*treeFile{}, dirs: map[string]os.FileMode{}}
}

// Put stores a generated file with default permissions.
func (t *Tree) Put(path string, data []byte) {
	t.files[path] = &treeFile{data: data}
}

// PutFixed stores a file whose permission bits are set explicitly.
func (t *Tree) PutFixed(path string, data []byte, mode os.FileMode) {
	t.files[path] = &treeFile{data: data, mode: mode, fixed: true}
}

// Mkdir records a directory that must exist even when it stays empty.
func (t *Tree) Mkdir(path string) {
	if path == "" {
		return
	}
	if _, ok := t.dirs[path]; !ok {
		t.dirs[path] = 0
	}
}

// MkdirFixed records a directory with explicit permission bits.
func (t *Tree) MkdirFixed(path string, mode os.FileMode) { t.dirs[path] = mode }

func (t *Tree) Has(path string) bool {
	_, ok := t.files[path]
	return ok
}

func (t *Tree) Data(path string) []byte {
	if file, ok := t.files[path]; ok {
		return file.data
	}
	return nil
}

// Mode is the legacy template mode. Default files actually use 0666 filtered by
// umask; plan descriptors must use the file's fixed flag instead of this value.
func (t *Tree) Mode(path string) os.FileMode {
	file := t.files[path]
	if file == nil {
		return 0
	}
	if file.fixed {
		return file.mode
	}
	return defaultFileMode
}

// Paths returns every file path in bytewise order.
func (t *Tree) Paths() []string {
	paths := make([]string, 0, len(t.files))
	for path := range t.files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func (t *Tree) directoryModes() map[string]os.FileMode {
	dirs := map[string]os.FileMode{}
	for dir, mode := range t.dirs {
		dirs[dir] = mode
	}
	for path := range t.dirs {
		for dir := parent(path); dir != ""; dir = parent(dir) {
			if _, ok := dirs[dir]; !ok {
				dirs[dir] = 0
			}
		}
	}
	for path := range t.files {
		for dir := parent(path); dir != ""; dir = parent(dir) {
			if _, ok := dirs[dir]; !ok {
				dirs[dir] = 0
			}
		}
	}
	return dirs
}

// Commit writes the tree below root, which must already exist.
func (t *Tree) Commit(root string) error {
	dirs := t.directoryModes()
	names := make([]string, 0, len(dirs))
	for dir := range dirs {
		names = append(names, dir)
	}
	sort.Slice(names, func(i, j int) bool {
		a, b := strings.Count(names[i], "/"), strings.Count(names[j], "/")
		return a < b || a == b && names[i] < names[j]
	})
	for _, dir := range names {
		if _, clash := t.files[dir]; clash {
			return failf("生成路径同时是文件和目录: %s", dir)
		}
		target := filepath.Join(root, filepath.FromSlash(dir))
		if err := os.MkdirAll(target, 0o777); err != nil {
			return err
		}
		if mode := dirs[dir]; mode != 0 {
			if err := os.Chmod(target, mode); err != nil {
				return err
			}
		}
	}
	for _, path := range t.Paths() {
		file := t.files[path]
		target := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(target), 0o777); err != nil {
			return err
		}
		if err := os.WriteFile(target, file.data, 0o666); err != nil {
			return err
		}
		if file.fixed {
			if err := os.Chmod(target, file.mode); err != nil {
				return err
			}
		}
	}
	return nil
}

// orderedPaths lists files the way a recursive readdir that sorts the names of
// each directory does: siblings are ordered by compare and directories are
// expanded in place.
func orderedPaths(paths []string, compare func(a, b string) int) []string {
	type node struct {
		children map[string]*node
		file     bool
	}
	root := &node{children: map[string]*node{}}
	for _, path := range paths {
		current := root
		parts := strings.Split(path, "/")
		for i, part := range parts {
			child := current.children[part]
			if child == nil {
				child = &node{children: map[string]*node{}}
				current.children[part] = child
			}
			if i == len(parts)-1 {
				child.file = true
			}
			current = child
		}
	}
	var out []string
	var visit func(prefix string, current *node)
	visit = func(prefix string, current *node) {
		names := make([]string, 0, len(current.children))
		for name := range current.children {
			names = append(names, name)
		}
		sort.SliceStable(names, func(i, j int) bool { return compare(names[i], names[j]) < 0 })
		for _, name := range names {
			child := current.children[name]
			path := name
			if prefix != "" {
				path = prefix + "/" + name
			}
			if child.file && len(child.children) == 0 {
				out = append(out, path)
			} else {
				visit(path, child)
			}
		}
	}
	visit("", root)
	return out
}

func bytewise(a, b string) int { return strings.Compare(a, b) }
