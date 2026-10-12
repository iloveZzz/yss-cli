package kernel

import (
	"sort"
)

const CiConfigRef = ".template-spec/process/project-ci.yaml"

func UniqueStrings(values []string) []string {
	set := map[string]bool{}
	for _, s := range values {
		set[s] = true
	}
	out := []string{}
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
