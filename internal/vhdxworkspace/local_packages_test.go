package vhdxworkspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const testLocalPackageName = "com.example.portable-connector"

func TestLocalPackageOverrideEmbedsWorkspaceWithoutMutatingSource(t *testing.T) {
	project := makeLocalPackageProject(t)
	localPackage := makeLocalPackage(t, t.TempDir(), testLocalPackageName, "payload-v1")
	originalManifest := mustReadTestFile(t, filepath.Join(project, "Packages", "manifest.json"))
	originalLock := mustReadTestFile(t, filepath.Join(project, "Packages", "packages-lock.json"))

	resolved, digest, err := ResolveLocalPackageOverrides(map[string]string{testLocalPackageName: localPackage})
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved) != 1 || digest == "" || resolved[0].ContentDigest == "" {
		t.Fatalf("resolved=%+v digest=%q", resolved, digest)
	}
	if err := ApplyLocalPackageOverrides(context.Background(), project, resolved); err != nil {
		t.Fatal(err)
	}
	if got := string(mustReadTestFile(t, filepath.Join(project, "Packages", testLocalPackageName, "Runtime.cs"))); got != "payload-v1" {
		t.Fatalf("embedded payload=%q", got)
	}
	manifest := mustReadTestJSON(t, filepath.Join(project, "Packages", "manifest.json"))
	if _, exists := manifest["dependencies"].(map[string]any)[testLocalPackageName]; exists {
		t.Fatal("embedded dependency remained in manifest")
	}
	lock := mustReadTestJSON(t, filepath.Join(project, "Packages", "packages-lock.json"))
	entry := lock["dependencies"].(map[string]any)[testLocalPackageName].(map[string]any)
	if entry["version"] != "file:"+testLocalPackageName || entry["source"] != "embedded" || entry["depth"] != float64(0) {
		t.Fatalf("lock entry=%+v", entry)
	}
	if _, exists := entry["hash"]; exists {
		t.Fatal("embedded lock entry retained hash")
	}
	if string(originalManifest) == string(mustReadTestFile(t, filepath.Join(project, "Packages", "manifest.json"))) || string(originalLock) == string(mustReadTestFile(t, filepath.Join(project, "Packages", "packages-lock.json"))) {
		t.Fatal("workspace metadata was not rewritten")
	}
}

func TestLocalPackageDigestIsContentBasedAndInvalidatesParentKey(t *testing.T) {
	project := makeKeyProject(t)
	firstRoot := t.TempDir()
	secondRoot := t.TempDir()
	firstPath := makeLocalPackage(t, firstRoot, testLocalPackageName, "same")
	secondPath := makeLocalPackage(t, secondRoot, testLocalPackageName, "same")
	_, firstDigest, err := ResolveLocalPackageOverrides(map[string]string{testLocalPackageName: firstPath})
	if err != nil {
		t.Fatal(err)
	}
	_, secondDigest, err := ResolveLocalPackageOverrides(map[string]string{testLocalPackageName: secondPath})
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest != secondDigest {
		t.Fatalf("source path affected digest: %s != %s", firstDigest, secondDigest)
	}
	firstKey, err := ComputeCompatibilityKeyWithLocalPackages(project, "unity", firstDigest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secondPath, "Runtime.cs"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	_, changedDigest, err := ResolveLocalPackageOverrides(map[string]string{testLocalPackageName: secondPath})
	if err != nil {
		t.Fatal(err)
	}
	changedKey, err := ComputeCompatibilityKeyWithLocalPackages(project, "unity", changedDigest)
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest == changedDigest || firstKey.Digest == changedKey.Digest {
		t.Fatal("local package content change did not invalidate parent key")
	}
}

func TestLocalPackageOverrideRejectsUnsafeIdentityAndDestinationReuse(t *testing.T) {
	packagePath := makeLocalPackage(t, t.TempDir(), testLocalPackageName, "payload")
	if _, _, err := ResolveLocalPackageOverrides(map[string]string{"../escape": packagePath}); err == nil {
		t.Fatal("unsafe package name accepted")
	}
	if _, _, err := ResolveLocalPackageOverrides(map[string]string{"com.example.wrong": packagePath}); err == nil {
		t.Fatal("package identity mismatch accepted")
	}
	project := makeLocalPackageProject(t)
	resolved, _, err := ResolveLocalPackageOverrides(map[string]string{testLocalPackageName: packagePath})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(project, "Packages", testLocalPackageName), 0700); err != nil {
		t.Fatal(err)
	}
	if err := ApplyLocalPackageOverrides(context.Background(), project, resolved); err == nil {
		t.Fatal("pre-existing embedded package destination accepted")
	}
}

func makeLocalPackageProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "Packages"), 0700); err != nil {
		t.Fatal(err)
	}
	manifest := `{"dependencies":{"` + testLocalPackageName + `":"file:/non-portable/path"}}`
	lock := `{"dependencies":{"` + testLocalPackageName + `":{"version":"file:/non-portable/path","depth":0,"source":"local","hash":"old"}}}`
	if err := os.WriteFile(filepath.Join(root, "Packages", "manifest.json"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Packages", "packages-lock.json"), []byte(lock), 0600); err != nil {
		t.Fatal(err)
	}
	return root
}

func makeLocalPackage(t *testing.T, parent, name, payload string) string {
	t.Helper()
	root := filepath.Join(parent, "package")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	packageJSON := `{"name":"` + name + `","version":"1.2.3"}`
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(packageJSON), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Runtime.cs"), []byte(payload), 0600); err != nil {
		t.Fatal(err)
	}
	return root
}

func mustReadTestFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func mustReadTestJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(mustReadTestFile(t, path), &value); err != nil {
		t.Fatal(err)
	}
	return value
}
