package refsworkspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCompatibilityKeyIncludesAssetsAndExecutableIdentity(t *testing.T) {
	project, editor := makeKeyFixture(t)
	options := CompatibilityOptions{
		ProjectPath:      project,
		UnityExecutable:  editor,
		BuildTarget:      "StandaloneWindows64",
		ScriptingBackend: "IL2CPP",
	}
	first, metrics, err := ComputeCompatibilityKey(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := ComputeCompatibilityKey(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("key is not deterministic:\n%+v\n%+v", first, second)
	}
	if metrics.KeyComputationMs < 0 || metrics.AssetsHashMs < 0 || metrics.PackagesHashMs < 0 || metrics.ProjectSettingsHashMs < 0 {
		t.Fatalf("invalid metrics: %+v", metrics)
	}
	if err := os.WriteFile(filepath.Join(project, "Assets", "asset.txt"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	assetsChanged, _, err := ComputeCompatibilityKey(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if assetsChanged.Digest == first.Digest || assetsChanged.AssetsSHA256 == first.AssetsSHA256 {
		t.Fatal("Assets change did not invalidate compatibility key")
	}
	if err := os.WriteFile(editor, []byte("different editor"), 0700); err != nil {
		t.Fatal(err)
	}
	editorChanged, _, err := ComputeCompatibilityKey(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if editorChanged.UnityExecutableSHA256 == assetsChanged.UnityExecutableSHA256 {
		t.Fatal("Unity executable content did not affect identity")
	}
}

func TestCompatibilityKeyRequiresExplicitRuntimeDimensions(t *testing.T) {
	project, editor := makeKeyFixture(t)
	_, _, err := ComputeCompatibilityKey(context.Background(), CompatibilityOptions{ProjectPath: project, UnityExecutable: editor})
	if ErrorCode(err) != CodeInvalidConfiguration {
		t.Fatalf("err=%v", err)
	}
}

func makeKeyFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	project := filepath.Join(root, "project")
	for _, dir := range []string{"Assets", "Packages", "ProjectSettings"} {
		if err := os.MkdirAll(filepath.Join(project, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"Assets/asset.txt":                      "asset",
		"Packages/manifest.json":                "{}\n",
		"Packages/packages-lock.json":           "{}\n",
		"ProjectSettings/ProjectVersion.txt":    "m_EditorVersion: 6000.1.0f1\n",
		"ProjectSettings/ProjectSettings.asset": "PlayerSettings:\n  scriptingBackend: 1\n",
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(project, filepath.FromSlash(name)), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	editor := filepath.Join(root, "Unity.exe")
	if err := os.WriteFile(editor, []byte("editor"), 0700); err != nil {
		t.Fatal(err)
	}
	return project, editor
}
