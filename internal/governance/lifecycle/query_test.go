package lifecycle

import (
	"errors"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/domain"
)

// verify 由 governance.RunContext 分派到原生语义校验；结构校验入口必须拒绝，不能降级为 Schema 检查。
func TestAssetCheckRefusesVerify(t *testing.T) {
	for _, group := range []string{"contract", "evidence", "handoff"} {
		_, err := AssetCheck(group, "verify", t.TempDir(), map[string]string{"schema": "s.json", "file": "f.yaml"})
		var de *domain.Error
		if !errors.As(err, &de) || de.Code != "INTERNAL" {
			t.Errorf("%s verify: %v", group, err)
		}
	}
}
