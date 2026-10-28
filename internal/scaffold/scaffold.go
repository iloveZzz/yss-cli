// Package scaffold generates the DDD and layered MVC backend project skeletons.
//
// It is the Go port of the yss-ddd-scaffold-generator and
// yss-layered-mvc-scaffold-generator skills; templates and wrapper assets are
// read from the embedded Spec Bundle, never from a second copy.
package scaffold

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

const manifestRef = ".yss/scaffold-generation.json"

// Options are the generator inputs. Empty strings mean "not supplied".
type Options struct {
	Architecture string `json:"architecture"` // "ddd" or "mvc"

	ProjectName string `json:"projectName"`
	BasePackage string `json:"basePackage"`
	OutputDir   string `json:"outputDir"`

	PlatformProfile   string `json:"platformProfile,omitempty"`
	SpringBootVersion string `json:"springBootVersion,omitempty"`
	JavaVersion       string `json:"javaVersion,omitempty"`

	GroupID              string `json:"groupId,omitempty"`
	ProjectVersion       string `json:"projectVersion,omitempty"`
	ParentGroupID        string `json:"parentGroupId,omitempty"`
	ParentArtifactID     string `json:"parentArtifactId,omitempty"`
	ParentVersion        string `json:"parentVersion,omitempty"`
	YssComponentsVersion string `json:"yssComponentsVersion,omitempty"`

	ContractFile     string `json:"contractFile,omitempty"`
	ContractID       string `json:"contractId,omitempty"`
	ContractVersion  string `json:"contractVersion,omitempty"`
	ApprovalRef      string `json:"approvalRef,omitempty"`
	CompilerDraftRef string `json:"compilerDraftRef,omitempty"`
	PersistedRef     string `json:"persistedRef,omitempty"`

	// Standalone is the explicit --standalone switch; it conflicts with contract input.
	Standalone bool `json:"standalone"`
	// WithExample selects the reference example; nil keeps the generator default
	// (on for standalone input, taken from the contract in the formal path).
	WithExample *bool `json:"withExample"`
	Force       bool  `json:"force,omitempty"`

	// Source overrides the embedded Spec Bundle (nil uses BundleSource).
	Source Source `json:"-"`

	// PlatformCatalog replaces the Bundle's platform catalog and AllowCandidate
	// accepts platform combinations that are not qualified (the manifest then
	// records platform_verification=candidate). They exist for maintenance
	// parity runs; the CLI never sets them.
	PlatformCatalog []byte `json:"-"`
	AllowCandidate  bool   `json:"-"`
	renderContext   *RenderContext
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
	if ctx == nil {
		return nil, fail("取消上下文不可为空")
	}
	if err := normalizeOptions(&o); err != nil {
		return nil, err
	}
	o.renderContext = captureRenderContext()
	src := o.Source
	if src == nil {
		var err error
		if src, err = BundleSource(); err != nil {
			return nil, err
		}
	}
	rendered, err := renderProject(ctx, src, &o)
	if err != nil {
		return nil, err
	}
	return publishRendered(ctx, &o, rendered)
}

func normalizeOptions(o *Options) error {
	if o.OutputDir != "" {
		abs, err := filepath.Abs(o.OutputDir)
		if err != nil {
			return err
		}
		o.OutputDir = abs
	}
	if o.ContractFile != "" {
		abs, err := filepath.Abs(o.ContractFile)
		if err != nil {
			return err
		}
		o.ContractFile = abs
	}
	return nil
}

type renderedProject struct {
	tree     *Tree
	manifest *OMap
	modules  []string
}

func renderProject(ctx context.Context, src Source, o *Options) (*renderedProject, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch o.Architecture {
	case "ddd":
		return renderDDD(ctx, src, o)
	case "mvc":
		return renderMVC(ctx, src, o)
	}
	return nil, fail("unsupported architecture: " + o.Architecture)
}

func guardTarget(o *Options) error {
	if _, err := exec.LookPath("git"); err != nil {
		return domain.Fail("CAPABILITY", "脚手架目录保护需要 PATH 中的 Git")
	}
	for _, part := range strings.Split(filepath.ToSlash(o.OutputDir), "/") {
		if strings.EqualFold(part, ".git") {
			return domain.Fail("ARGUMENT", "脚手架输出不得位于 Git 内部目录")
		}
	}
	final, err := safefs.Path(o.OutputDir, o.ProjectName)
	if err != nil {
		return err
	}
	gitRoot := findGitRoot(final)
	if gitRoot == "" {
		gitRoot = findGitRoot(o.OutputDir)
	}
	if message := gitSubmoduleScaffoldViolation(gitRoot, o.OutputDir, o.ProjectName); message != "" {
		return fail(message)
	}
	if message := overlayMountViolation(gitRoot, final, true); message != "" {
		return fail(message)
	}
	if _, err = os.Lstat(final); err == nil {
		return failf("unsupported: initialize-only 脚手架只创建全新项目，拒绝已存在目标: %s", final)
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

func publishRendered(ctx context.Context, o *Options, rendered *renderedProject) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := guardTarget(o); err != nil {
		return nil, err
	}
	staging, err := stage(o.OutputDir, o.ProjectName)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)
	// Recheck after staging: a concurrently occupied target must stay untouched.
	if err := guardTarget(o); err != nil {
		return nil, err
	}
	root, err := publish(rendered.tree, staging, o.OutputDir, o.ProjectName)
	if err != nil {
		return nil, err
	}
	return &Result{ProjectRoot: root, ManifestPath: filepath.Join(root, filepath.FromSlash(manifestRef)), Manifest: rendered.manifest, Modules: rendered.modules}, nil
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
	if err := guardTarget(&Options{OutputDir: outputDir, ProjectName: projectName}); err != nil {
		return "", err
	}
	final := filepath.Join(outputDir, projectName)
	if err := publishNoReplace(projectRoot, final); err != nil {
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
