package kernel

import (
	"github.com/iloveZzz/yss-cli/internal/schema"
	"path/filepath"
	"strings"
)

func ContractN(v any) int { n, _ := Integer(v); return int(n) }

func ContractPath(v string) bool {
	if v == "" || filepath.IsAbs(v) || strings.ContainsAny(v, "\\\x00\n\r\t*?{}[]") || strings.Contains(v, ":") {
		return false
	}
	for _, p := range strings.Split(v, "/") {
		if p == ".." || p == "" || strings.TrimRight(p, " .") != p {
			return false
		}
	}
	return true
}

func ContractTicketMetadata(s *SemanticSession, ref string) (map[string]any, error) {
	raw, e := s.Bytes(ref)
	if e != nil {
		return nil, e
	}
	raw = []byte(strings.TrimPrefix(string(raw), "\ufeff"))
	hasHeader := strings.HasPrefix(string(raw), "---\n") || strings.HasPrefix(string(raw), "---\r\n")
	if hasHeader {
		raw, _, e = Frontmatter(raw)
		if e != nil {
			return nil, s.Unavailable("INPUT", ref+": "+e.Error())
		}
	}
	v, e := schema.Parse(raw)
	if e != nil && hasHeader {
		return nil, s.Unavailable("INPUT", ref+": "+e.Error())
	}
	m, _ := Object(v)
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}
