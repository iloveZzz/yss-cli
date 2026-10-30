//go:build yss_nobundle

package trustguard

import (
	"github.com/iloveZzz/yss-cli/internal/identitymeta"
	"testing"
)

func TestMissingTrustedBundleFailsClosed(t *testing.T) {
	if _, err := Open(&identitymeta.Metadata{Profile: "spec"}); err == nil {
		t.Fatal("missing trusted Bundle accepted")
	}
}
