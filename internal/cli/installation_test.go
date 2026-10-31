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

func TestCapabilitiesDoNotAdvertiseProgramRecovery(t *testing.T) {
	for _, name := range capabilitiesBase()["native"].([]string) {
		if name == "program-update-recover-and-rollback" {
			t.Fatal("removed program recovery must not be advertised")
		}
	}
}
