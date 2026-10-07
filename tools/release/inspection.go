package release

import (
	"encoding/json"
	"path"
	"strings"
)

func completeInspection(raw []byte, profile string) error {
	var o map[string]any
	if err := json.Unmarshal(raw, &o); err != nil || (o["schemaVersion"] != float64(2) && o["schemaVersion"] != float64(3)) || o["profile"] != profile || len(object(o["manifest"])) == 0 || object(o["distribution"]) == nil || len(object(o["files"])) == 0 || len(stringsOf(o["initialPaths"])) == 0 {
		return reject("EVIDENCE", "complete public bundle inspect result required")
	}
	producer, policy := object(o["producer"]), object(o["sourcePolicy"])
	if producer["sourceState"] != "committed" || !commitPattern.MatchString(text(producer["commit"])) || text(producer["version"]) == "" || policy["kind"] != "committed" || !shaPattern.MatchString(text(policy["digest"])) || text(policy["path"]) == "" {
		return reject("PROVENANCE", "complete bundle producer/policy provenance required")
	}
	for ref, v := range object(o["files"]) {
		f := object(v)
		mode, mok := f["mode"].(float64)
		size, sok := f["size"].(float64)
		if path.Clean(ref) != ref || path.IsAbs(ref) || strings.HasPrefix(ref, "../") || strings.ContainsAny(ref, "\\:\x00") || !shaPattern.MatchString(text(f["digest"])) || !mok || mode < 0 || mode > 0777 || mode != float64(uint32(mode)) || !sok || size < 0 || size != float64(int64(size)) || text(f["ownership"]) == "" {
			return reject("EVIDENCE", "invalid complete bundle file descriptor")
		}
	}
	for _, ref := range stringsOf(o["initialPaths"]) {
		if object(o["files"])[ref] == nil {
			return reject("EVIDENCE", "initial bundle path missing from full inspection")
		}
	}
	return nil
}
