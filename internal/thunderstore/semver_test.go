package thunderstore

import "testing"

func TestCompareVersionsUsesNumericParts(t *testing.T) {
	compare, err := CompareVersions("1.0.10", "1.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if compare <= 0 {
		t.Fatalf("expected 1.0.10 to be newer than 1.0.1, got %d", compare)
	}
}

func TestParseDependency(t *testing.T) {
	ref, err := ParseDependency("denikson-BepInExPack_Valheim-5.4.2202")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Namespace != "denikson" || ref.Name != "BepInExPack_Valheim" || ref.Version != "5.4.2202" {
		t.Fatalf("unexpected ref: %#v", ref)
	}
}
