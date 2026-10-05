package updater

import "testing"

func TestProgramVersionOrderingIsIndependentOfProfileLegacyVersion(t *testing.T) {
	ordered := []string{"0.9.9", "1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0", "1.0.1", "999999999999999999999999.0.0"}
	for i := 0; i < len(ordered)-1; i++ {
		n, e := compareVersions(ordered[i], ordered[i+1])
		if e != nil || n >= 0 {
			t.Fatalf("order %s %s: %d %v", ordered[i], ordered[i+1], n, e)
		}
	}
	if n, e := compareVersions("1.0.0+build.1", "1.0.0+build.2"); e != nil || n != 0 {
		t.Fatal("build labels changed precedence")
	}
	for _, v := range []string{"v1.0.0", "1.00.0", "1.0.0-alpha.01", "1.0", "1.0.0!", ""} {
		if _, e := versionParts(v); e == nil {
			t.Fatal("invalid version accepted: " + v)
		}
	}
}
