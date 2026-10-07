// Package worklayout resolves project-owned feature paths from the tracker contract.
package worklayout

import (
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"os"
	"regexp"
	"sort"
	"strings"
)

const TrackerRef = ".template-spec/agents/issue-tracker.md"
const DefaultRoot = ".work"

var HistoricalRoots = []string{"docs/.scratch", ".scratch", "docs/requirements/tickets"}
var featurePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
var checkpointPattern = regexp.MustCompile(`^([a-z0-9][a-z0-9-]*)/[^/]+\.(?:json|ya?ml)$`)
var ticketPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*/issues/[^/]+\.md$`)

// ReadScanRoots supports read-only protection before initialization. It never
// chooses a writing root; project asset consumers must call Read instead.
func ReadScanRoots(root string) ([]string, error) {
	file, err := safefs.Path(root, TrackerRef)
	if err != nil {
		return nil, err
	}
	_, err = os.Lstat(file)
	roots := append([]string{DefaultRoot}, HistoricalRoots...)
	if err == nil {
		layout, err := Read(root)
		if err != nil {
			return nil, err
		}
		roots = layout.ScanRoots
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	for _, ref := range roots {
		if _, err := safefs.Path(root, ref); err != nil {
			return nil, err
		}
	}
	sort.Strings(roots)
	return roots, nil
}

type Layout struct {
	Root        string
	ScanRoots   []string
	projectRoot string
}

func reserved(ref string) bool {
	switch strings.ToLower(strings.Split(ref, "/")[0]) {
	case ".git", ".yss", ".agents", ".codex", ".cursor", ".pi", ".github", ".vscode", ".template-spec", ".template-source", "apps", "app", "packages", "node_modules":
		return true
	}
	return false
}
func New(root string, tracker map[string]any) (*Layout, error) {
	base, ok := tracker["root"].(string)
	platform, _ := tracker["platform"].(string)
	if !ok || (platform != "local-markdown" && platform != "github" && platform != "gitlab") {
		return nil, domain.Fail("WORK_LAYOUT_CONFIG", "tracker.root required")
	}
	base = strings.TrimSuffix(base, "/")
	file, e := safefs.Path(root, base)
	if e != nil {
		return nil, e
	}
	if st, e := os.Stat(file); e == nil && !st.IsDir() {
		return nil, domain.Fail("WORK_LAYOUT_CONFIG", "root is not a directory")
	} else if e != nil && !os.IsNotExist(e) {
		return nil, e
	}
	if reserved(base) {
		return nil, domain.Fail("WORK_LAYOUT_RESERVED", base)
	}
	if strings.EqualFold(base, ".scratch") || strings.EqualFold(base, "docs/requirements/tickets") {
		return nil, domain.Fail("WORK_LAYOUT_MIGRATION_REQUIRED", base)
	}
	roots := map[string]bool{base: true, DefaultRoot: true}
	for _, ref := range HistoricalRoots {
		roots[ref] = true
	}
	if value, exists := tracker["legacy_roots"]; exists {
		entries, ok := value.([]any)
		if !ok {
			return nil, domain.Fail("WORK_LAYOUT_CONFIG", "legacy_roots must be an array")
		}
		for _, v := range entries {
			ref, ok := v.(string)
			if !ok {
				return nil, domain.Fail("WORK_LAYOUT_CONFIG", "legacy_roots must be an array")
			}
			if e := safefs.ValidateRef(ref); e != nil {
				return nil, e
			}
			if reserved(ref) {
				return nil, domain.Fail("WORK_LAYOUT_RESERVED", ref)
			}
			roots[ref] = true
		}
	}
	list := []string{}
	for ref := range roots {
		list = append(list, ref)
	}
	sort.Strings(list)
	return &Layout{Root: base, ScanRoots: list, projectRoot: root}, nil
}
func FromDocument(root string, data []byte) (*Layout, error) {
	source := string(data)
	match := regexp.MustCompile(`(?s)^---\r?\n(.*?)\r?\n---(?:\r?\n|$)`).FindStringSubmatch(source)
	if match == nil {
		return nil, domain.Fail("WORK_LAYOUT_CONFIG", "tracker frontmatter required")
	}
	value, e := schema.Parse([]byte(match[1]))
	if e != nil {
		return nil, e
	}
	document, ok := value.(map[string]any)
	if !ok {
		return nil, domain.Fail("WORK_LAYOUT_CONFIG", "tracker config required")
	}
	tracker, ok := document["tracker"].(map[string]any)
	if !ok {
		return nil, domain.Fail("WORK_LAYOUT_CONFIG", "tracker config required")
	}
	return New(root, tracker)
}
func Read(root string) (*Layout, error) {
	file, e := safefs.Path(root, TrackerRef)
	if e != nil {
		return nil, e
	}
	b, e := os.ReadFile(file)
	if e != nil {
		return nil, e
	}
	return FromDocument(root, b)
}
func (l *Layout) FeatureRoot(feature string) (string, error) {
	if !featurePattern.MatchString(feature) {
		return "", domain.Fail("WORK_LAYOUT_FEATURE", feature)
	}
	ref := l.Root + "/" + feature
	_, e := safefs.Path(l.projectRoot, ref)
	return ref, e
}
func (l *Layout) CheckpointFeature(ref string) (string, error) {
	if _, e := safefs.Path(l.projectRoot, ref); e != nil {
		return "", e
	}
	if !strings.HasPrefix(ref, l.Root+"/") {
		return "", domain.Fail("WORK_LAYOUT_CHECKPOINT", ref+"; root="+l.Root)
	}
	match := checkpointPattern.FindStringSubmatch(strings.TrimPrefix(ref, l.Root+"/"))
	if match == nil {
		return "", domain.Fail("WORK_LAYOUT_CHECKPOINT", ref)
	}
	return match[1], nil
}
func (l *Layout) IsTicket(ref string) bool {
	if _, e := safefs.Path(l.projectRoot, ref); e != nil {
		return false
	}
	return strings.HasPrefix(ref, l.Root+"/") && ticketPattern.MatchString(strings.TrimPrefix(ref, l.Root+"/"))
}
func (l *Layout) FeatureOf(ref string) (string, error) {
	if _, e := safefs.Path(l.projectRoot, ref); e != nil {
		return "", e
	}
	if !strings.HasPrefix(ref, l.Root+"/") {
		return "", domain.Fail("WORK_LAYOUT_FEATURE", ref)
	}
	feature := strings.Split(strings.TrimPrefix(ref, l.Root+"/"), "/")[0]
	_, e := l.FeatureRoot(feature)
	return feature, e
}
