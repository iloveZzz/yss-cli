package bundle

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestReconstructBaselineRejectsAlteredObjects(t *testing.T) {
	zip := func(raw []byte) []byte {
		var out bytes.Buffer
		z := gzip.NewWriter(&out)
		_, _ = z.Write(raw)
		_ = z.Close()
		return out.Bytes()
	}
	key := strings.Repeat("a", 64)
	prefix := []byte(`{"storageFormatVersion":2,"profiles":{},"objects":{`)
	baseline := []byte(string(prefix) + `"` + key + `":"YWJj"}}`)
	digest := fmt.Sprintf("%x", sha256.Sum256(baseline))
	current := []byte(`{"objects":{"` + key + `":"YWJj"}}`)
	patch, _ := json.Marshal(map[string]any{"version": 1, "prefix": base64.StdEncoding.EncodeToString(prefix), "keys": []string{key}, "missing": map[string]string{}})
	got, err := reconstructBaseline(zip(current), zip(patch), digest)
	if err != nil || !bytes.Equal(got, baseline) {
		t.Fatalf("基线重建失败: %s %v", got, err)
	}
	current = []byte(`{"objects":{"` + key + `":"dGFtcGVyZWQ="}}`)
	if _, err = reconstructBaseline(zip(current), zip(patch), digest); err == nil {
		t.Fatal("篡改的共享对象被接受")
	}
}
