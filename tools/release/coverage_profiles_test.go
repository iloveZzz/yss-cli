package release

import (
	"reflect"
	"testing"
)

func TestRequiredCoverageOnlySupportedProfiles(t *testing.T) {
	if got := RequiredGateCoverage("cli-integration"); !reflect.DeepEqual(got, []string{"cli.integration.spec", "cli.integration.backend"}) {
		t.Fatal(got)
	}
}
