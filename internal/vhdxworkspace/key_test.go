package vhdxworkspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAssetsChangeUpdatesEvidenceButNotCompatibilityKey(t *testing.T) {
	project := makeKeyProject(t)
	firstKey, err := ComputeCompatibilityKey(project, `C:\Unity\6000.3.8f1\Unity.exe`)
	if err != nil {
		t.Fatal(err)
	}
	firstSnapshot, err := ComputeSourceSnapshot(project)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "Assets", "new.asset"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	secondKey, err := ComputeCompatibilityKey(project, `C:\Unity\6000.3.8f1\Unity.exe`)
	if err != nil {
		t.Fatal(err)
	}
	secondSnapshot, err := ComputeSourceSnapshot(project)
	if err != nil {
		t.Fatal(err)
	}
	if firstKey.Digest != secondKey.Digest {
		t.Fatalf("Assets invalidated parent key: %s != %s", firstKey.Digest, secondKey.Digest)
	}
	if firstSnapshot.AssetsDigest == secondSnapshot.AssetsDigest || firstSnapshot.Digest == secondSnapshot.Digest {
		t.Fatal("Assets change was not captured as source evidence")
	}
}

func TestPackagesAndSettingsInvalidateCompatibilityKey(t *testing.T) {
	project := makeKeyProject(t)
	first, err := ComputeCompatibilityKey(project, "unity")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "Packages", "manifest.json"), []byte(`{"dependencies":{"x":"1"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	second, err := ComputeCompatibilityKey(project, "unity")
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest == second.Digest {
		t.Fatal("Packages change did not invalidate key")
	}
	if err := os.WriteFile(filepath.Join(project, "ProjectSettings", "ProjectSettings.asset"), []byte("scriptingBackend: 1"), 0600); err != nil {
		t.Fatal(err)
	}
	third, err := ComputeCompatibilityKey(project, "unity")
	if err != nil {
		t.Fatal(err)
	}
	if second.Digest == third.Digest {
		t.Fatal("ProjectSettings change did not invalidate key")
	}
}

func makeKeyProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"Assets", "Packages", "ProjectSettings"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"Assets/base.asset":                     "base",
		"Packages/manifest.json":                `{"dependencies":{}}`,
		"Packages/packages-lock.json":           `{"dependencies":{}}`,
		"ProjectSettings/ProjectVersion.txt":    "m_EditorVersion: 6000.3.8f1\n",
		"ProjectSettings/ProjectSettings.asset": "scriptingBackend: 0\n",
	}
	for path, value := range files {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(path)), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
