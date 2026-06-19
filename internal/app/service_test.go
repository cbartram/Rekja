package app

import "testing"

func TestParseTrackedSpecDefaultsToLatest(t *testing.T) {
	tracked, err := parseTrackedSpec("ValheimModding-Jotunn")
	if err != nil {
		t.Fatal(err)
	}
	if tracked.FullName != "ValheimModding-Jotunn" || tracked.DesiredVersion != "latest" {
		t.Fatalf("unexpected tracked mod: %#v", tracked)
	}
}

func TestParseTrackedSpecAllowsPinnedVersion(t *testing.T) {
	tracked, err := parseTrackedSpec("ValheimModding-Jotunn@2.24.3")
	if err != nil {
		t.Fatal(err)
	}
	if tracked.DesiredVersion != "2.24.3" {
		t.Fatalf("unexpected desired version: %s", tracked.DesiredVersion)
	}
}
