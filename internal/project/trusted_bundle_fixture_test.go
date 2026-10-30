//go:build !yss_nobundle

package project

import (
	"testing"

	"github.com/iloveZzz/yss-cli/internal/bundle"
)

func trustedBundle(t *testing.T, profile string) *bundle.Bundle {
	t.Helper()
	b, err := bundle.Load(profile)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
