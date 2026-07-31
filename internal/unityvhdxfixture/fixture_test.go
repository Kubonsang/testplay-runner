package unityvhdxfixture

import (
	"path/filepath"
	"testing"
)

func TestRepositoryFixtureIsMinimalAndPinned(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "testdata", "unity-vhdx-fixture"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateFixtureSource(root); err != nil {
		t.Fatal(err)
	}
	version, err := FixtureVersion(root)
	if err != nil {
		t.Fatal(err)
	}
	if version != TargetUnityVersion {
		t.Fatalf("fixture version=%q want=%q", version, TargetUnityVersion)
	}
}
