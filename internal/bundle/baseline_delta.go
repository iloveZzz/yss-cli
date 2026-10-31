package bundle

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/iloveZzz/yss-cli/internal/domain"
)

var baselineObjectKey = regexp.MustCompile(`^[a-f0-9]{64}$`)

func reconstructBaseline(currentGzip, deltaGzip []byte, expected string) ([]byte, error) {
	current, err := snapshots.load("baseline-current", currentGzip)
	if err != nil {
		return nil, err
	}
	delta, err := decompressSnapshot(deltaGzip)
	if err != nil {
		return nil, err
	}
	var pack struct {
		Objects map[string]string `json:"objects"`
	}
	var patch struct {
		Version int               `json:"version"`
		Prefix  []byte            `json:"prefix"`
		Keys    []string          `json:"keys"`
		Missing map[string]string `json:"missing"`
	}
	if err = json.Unmarshal(current, &pack); err != nil {
		return nil, err
	}
	if err = json.Unmarshal(delta, &patch); err != nil {
		return nil, err
	}
	if patch.Version != 1 || patch.Missing == nil || pack.Objects == nil {
		return nil, domain.Fail("BUNDLE", "基线差量格式错误")
	}
	var out bytes.Buffer
	out.Write(patch.Prefix)
	for i, key := range patch.Keys {
		if !baselineObjectKey.MatchString(key) {
			return nil, domain.Fail("BUNDLE", "基线差量对象无效")
		}
		value, ok := patch.Missing[key]
		if !ok {
			value, ok = pack.Objects[key]
		}
		if !ok {
			return nil, domain.Fail("BUNDLE", "基线差量对象缺失")
		}
		if i != 0 {
			out.WriteByte(',')
		}
		k, _ := json.Marshal(key)
		v, _ := json.Marshal(value)
		if out.Len()+len(k)+len(v)+4 > snapshotLimit {
			return nil, domain.Fail("BUNDLE", "基线快照超过限制")
		}
		out.Write(k)
		out.WriteByte(':')
		out.Write(v)
	}
	out.WriteString("}}")
	if fmt.Sprintf("%x", sha256.Sum256(out.Bytes())) != expected {
		return nil, domain.Fail("BUNDLE", "受信基线摘要不匹配")
	}
	return out.Bytes(), nil
}
