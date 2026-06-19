package sync

import "testing"

func TestCleanArchivePathRejectsTraversal(t *testing.T) {
	if _, err := cleanArchivePath("../evil.dll"); err == nil {
		t.Fatal("expected traversal path to be rejected")
	}
}

func TestCleanArchivePathAllowsNestedFiles(t *testing.T) {
	path, err := cleanArchivePath("plugins/MyMod.dll")
	if err != nil {
		t.Fatal(err)
	}
	if path != "plugins/MyMod.dll" {
		t.Fatalf("unexpected path: %s", path)
	}
}

func TestNormalizeInstallPathStripsPluginRoots(t *testing.T) {
	tests := map[string]string{
		"BepInEx/plugins/Jotunn.dll": "Jotunn.dll",
		"plugins/Nested/Mod.dll":     "Nested/Mod.dll",
		"BepInEx/config/Jotunn.cfg":  "config/Jotunn.cfg",
		"config/Mod.cfg":             "config/Mod.cfg",
	}

	for input, expected := range tests {
		if actual := normalizeInstallPath(input); actual != expected {
			t.Fatalf("normalizeInstallPath(%q) = %q, want %q", input, actual, expected)
		}
	}
}
