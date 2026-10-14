package reading

import (
	"sort"
)

func readingSortedKeys(v map[string]any) []string {
	keys := []string{}
	for key := range v {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
