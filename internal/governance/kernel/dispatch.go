package kernel

import "sort"

func RegisterSemanticValidator(kind string, fn SemanticValidator) {
	if kind == "" || fn == nil || SemanticValidators[kind] != nil {
		panic("duplicate or invalid semantic validator: " + kind)
	}
	SemanticValidators[kind] = fn
}

func SemanticOptions(args map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range args {
		out[k] = v
	}
	return out
}

func SemanticCoverage(s *SemanticSession, key string, value any) {
	if s.Report.Coverage == nil {
		s.Report.Coverage = map[string]any{}
	}
	s.Report.Coverage[key] = value
}

func SameStringSet(a, b any) bool {
	x, y := SemStrings(a), SemStrings(b)
	if len(x) == 0 || len(x) != len(y) {
		return false
	}
	sort.Strings(x)
	sort.Strings(y)
	for i := range x {
		if x[i] == "" || x[i] != y[i] || i > 0 && x[i] == x[i-1] {
			return false
		}
	}
	return true
}
