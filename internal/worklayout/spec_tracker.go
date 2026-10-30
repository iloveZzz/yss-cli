package worklayout

import (
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/identitymeta"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"net/url"
	"os"
	"regexp"
	"strings"
)

// SpecTracker is the minimal non-technical Spec destination, not lifecycle state.
type SpecTracker struct{ Platform, Root, Repository, Host, Project string }

func ReadSpecTracker(root string) (*SpecTracker, error) {
	file, err := safefs.Path(root, TrackerRef)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	return ParseSpecTracker(root, raw)
}

// ParseSpecTracker validates a candidate before initialization writes any files.
func ParseSpecTracker(root string, raw []byte) (*SpecTracker, error) {
	match := regexp.MustCompile(`(?s)^---\r?\n(.*?)\r?\n---(?:\r?\n|$)`).FindSubmatch(raw)
	if match == nil {
		return nil, domain.Fail("SPEC_TRACKER_CONFIG", "tracker frontmatter required")
	}
	v, err := schema.Parse(match[1])
	if err != nil {
		return nil, err
	}
	doc, ok := v.(map[string]any)
	if !ok {
		return nil, domain.Fail("SPEC_TRACKER_CONFIG", "tracker object required")
	}
	tracker, ok := doc["tracker"].(map[string]any)
	if !ok {
		return nil, domain.Fail("SPEC_TRACKER_CONFIG", "tracker object required")
	}
	for _, key := range []string{"platform", "root", "repository", "host", "project"} {
		if value, present := tracker[key]; present {
			if _, ok := value.(string); !ok {
				return nil, domain.Fail("SPEC_TRACKER_CONFIG", "tracker."+key+" must be a string")
			}
		}
	}
	text := func(k string) string { s, _ := tracker[k].(string); return s }
	out := &SpecTracker{Platform: text("platform"), Root: text("root"), Repository: text("repository"), Host: text("host"), Project: text("project")}
	if out.Root != "" {
		if err := safefs.ValidateRef(out.Root); err != nil {
			return nil, err
		}
		if reserved(out.Root) {
			return nil, domain.Fail("WORK_LAYOUT_RESERVED", out.Root)
		}
		p, err := safefs.Path(root, out.Root)
		if err != nil {
			return nil, err
		}
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return nil, domain.Fail("SPEC_TRACKER_CONFIG", "root is not a directory")
		} else if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	} else if _, present := tracker["root"]; present {
		return nil, domain.Fail("SPEC_TRACKER_CONFIG", "root must be a nonempty relative path")
	}
	switch out.Platform {
	case "local-markdown":
		if out.Root == "" {
			return nil, domain.Fail("SPEC_TRACKER_CONFIG", "local tracker.root required")
		}
	case "github":
		if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9_.-]+$`).MatchString(out.Repository) {
			return nil, domain.Fail("SPEC_TRACKER_CONFIG", "GitHub repository owner/repo required")
		}
	case "gitlab":
		u, err := url.Parse(out.Host)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return nil, domain.Fail("SPEC_TRACKER_CONFIG", "GitLab HTTPS host required")
		}
		if err := safefs.ValidateRef(out.Project); err != nil || !strings.Contains(out.Project, "/") {
			return nil, domain.Fail("SPEC_TRACKER_CONFIG", "GitLab project path required")
		}
	default:
		return nil, domain.Fail("SPEC_TRACKER_CONFIG", "unsupported tracker platform")
	}
	return out, nil
}

func remoteSpecScanAllowed(root string) (bool, error) {
	file, err := safefs.Path(root, domain.MetadataFile)
	if err != nil {
		return false, err
	}
	raw, err := os.ReadFile(file)
	if os.IsNotExist(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	meta, err := identitymeta.ValidateNative(root, raw, "")
	if err != nil {
		return false, err
	}
	workflow, err := identitymeta.SpecWorkflow(meta)
	return workflow == identitymeta.MattSpecWorkflow, err
}
