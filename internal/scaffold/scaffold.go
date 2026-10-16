// Package scaffold generates the DDD and layered MVC backend project skeletons.
//
// It is the Go port of the yss-ddd-scaffold-generator and
// yss-layered-mvc-scaffold-generator skills; templates and wrapper assets are
// read from the embedded Spec Bundle, never from a second copy.
package scaffold

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

const manifestRef = ".yss/scaffold-generation.json"

// Options are the generator inputs. Empty strings mean "not supplied".
type Options struct {
	Architecture string // "ddd" or "mvc"

	ProjectName string
	BasePackage string
	OutputDir   string

	PlatformProfile   string
	SpringBootVersion string
	JavaVersion       string

	GroupID              string
	ProjectVersion       string
	ParentGroupID        string
	ParentArtifactID     string
	ParentVersion        string
	YssComponentsVersion string

	ContractFile     string
	ContractID       string
	ContractVersion  string
	ApprovalRef      string
	CompilerDraftRef string
	PersistedRef     string

	// Standalone is the explicit --standalone switch; it conflicts with contract input.
	Standalone bool
	// WithExample selects the reference example; nil keeps the generator default
	// (on for standalone input, taken from the contract in the formal path).
	WithExample *bool
	Force       bool

	// Source overrides the embedded Spec Bundle (nil uses BundleSource).
	Source Source
}

// Result describes a generated project.
type Result struct {
	ProjectRoot  string
	ManifestPath string
	Manifest     *OMap
	Modules      []string
}

func (o *Options) hasContract() bool {
	for _, value := range []string{o.ContractFile, o.ContractID, o.ContractVersion, o.ApprovalRef, o.CompilerDraftRef, o.PersistedRef} {
		if value != "" {
			return true
		}
	}
	return false
}

func (o *Options) platformSupplied() bool {
	return o.PlatformProfile != "" || o.SpringBootVersion != "" || o.JavaVersion != ""
}

func (o *Options) example() bool { return o.WithExample == nil || *o.WithExample }

// Run generates the project described by o.
func Run(ctx context.Context, o Options) (*Result, error) {
	src := o.Source
	if src == nil {
		var err error
		if src, err = BundleSource(); err != nil {
			return nil, err
		}
	}
	if o.OutputDir != "" {
		abs, err := filepath.Abs(o.OutputDir)
		if err != nil {
			return nil, err
		}
		o.OutputDir = abs
	}
	if o.ContractFile != "" {
		abs, err := filepath.Abs(o.ContractFile)
		if err != nil {
			return nil, err
		}
		o.ContractFile = abs
	}
	switch o.Architecture {
	case "ddd":
		return runDDD(ctx, src, &o)
	case "mvc":
		return runMVC(ctx, src, &o)
	}
	return nil, fail("unsupported architecture: " + o.Architecture)
}

// stage creates the output directory and an owner-only staging directory next to
// the final project, so the project appears with a single rename.
func stage(outputDir, projectName string) (staging string, err error) {
	if err = os.MkdirAll(outputDir, 0o777); err != nil {
		return "", err
	}
	return os.MkdirTemp(outputDir, "."+projectName+".staging-")
}

// publish writes the tree into staging and moves the project into place.
func publish(tree *Tree, staging, outputDir, projectName string) (string, error) {
	projectRoot := filepath.Join(staging, projectName)
	if err := os.MkdirAll(projectRoot, 0o777); err != nil {
		return "", err
	}
	if err := tree.Commit(projectRoot); err != nil {
		return "", err
	}
	final := filepath.Join(outputDir, projectName)
	if err := os.Rename(projectRoot, final); err != nil {
		return "", err
	}
	return final, nil
}

var binaryExtensions = map[string]bool{".class": true, ".db": true, ".jar": true, ".png": true, ".jpg": true, ".jpeg": true, ".gif": true}

// checkPlaceholders rejects unresolved template placeholders and plain-text
// credentials in any generated text file.
func checkPlaceholders(tree *Tree) error {
	for _, path := range tree.Paths() {
		if binaryExtensions[filepath.Ext(path)] {
			continue
		}
		text := string(tree.Data(path))
		if strings.Contains(text, "{{") || strings.Contains(text, "root/root") {
			return failf("生成文件包含未替换占位符或明文凭据: %s", path)
		}
	}
	return nil
}
