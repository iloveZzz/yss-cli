package cli

import "testing"

func TestInstallationCapability(t *testing.T) {
	for _, name := range capabilitiesBase()["native"].([]string) {
		if name == "installation-v1" {
			return
		}
	}
	t.Fatal("installation support is undiscoverable")
}
