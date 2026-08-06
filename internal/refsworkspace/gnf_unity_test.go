package refsworkspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFreezeGNFTestSelectionUsesDeterministicFallback(t *testing.T) {
	root := makeGNFSourceTree(t, unityVersionForTest)
	selection, err := freezeGNFTestSelection(root)
	if err != nil {
		t.Fatal(err)
	}
	if selection.HistoricalFound {
		t.Fatal("historical test unexpectedly found")
	}
	if len(selection.EditMode) != 1 || selection.EditMode[0] != "GNF.DungeonGen.Tests.WallPropValidatorTests.NullPrefab_Error" {
		t.Fatalf("edit selection=%v", selection.EditMode)
	}
	if len(selection.PlayMode) != 1 || selection.PlayMode[0] != "DOOR_CONSENSUS_Tests.Proximity_CountsNearestExitWithinRadius" {
		t.Fatalf("play selection=%v", selection.PlayMode)
	}
	if selection.FrozenAt.IsZero() || !strings.Contains(selection.Reason, "offline deterministic") {
		t.Fatalf("selection was not frozen with fallback reason: %+v", selection)
	}
}

func TestFreezeGNFTestSelectionRejectsMissingSelectedTest(t *testing.T) {
	root := makeGNFSourceTree(t, unityVersionForTest)
	if err := os.Remove(filepath.Join(root, "Assets", "Tests", "PlayMode", "DOOR_CONSENSUS_Tests.cs")); err != nil {
		t.Fatal(err)
	}
	_, err := freezeGNFTestSelection(root)
	if ErrorCode(err) != CodeDeterministicTestUnavailable {
		t.Fatalf("err=%v", err)
	}
}

func TestCopyGNFProjectInputsPhysicallyIsolatesPackages(t *testing.T) {
	root := makeGNFSourceTree(t, unityVersionForTest)
	destination := filepath.Join(t.TempDir(), "workspace")
	if err := copyGNFProjectInputs(context.Background(), root, destination); err != nil {
		t.Fatal(err)
	}
	lockSource := filepath.Join(root, "Packages", "packages-lock.json")
	lockCopy := filepath.Join(destination, "Packages", "packages-lock.json")
	if info, err := os.Lstat(filepath.Join(destination, "Packages")); err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("Packages was not a real copied directory: info=%v err=%v", info, err)
	}
	if err := os.WriteFile(lockCopy, []byte(`{"worker":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(lockSource)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{}` {
		t.Fatalf("source packages-lock mutated: %s", data)
	}
	if err := copyGNFProjectInputs(context.Background(), root, destination); err == nil {
		t.Fatal("existing workspace should be rejected")
	}
}

func TestPrepareGNFWorkspacesRootCreatesOwnedParentForReferenceAndWorker(t *testing.T) {
	artifactRoot := t.TempDir()
	workspaces, err := prepareGNFWorkspacesRoot(artifactRoot)
	if err != nil {
		t.Fatal(err)
	}
	if workspaces != filepath.Join(artifactRoot, "workspaces") {
		t.Fatalf("workspaces=%q", workspaces)
	}
	info, err := os.Lstat(workspaces)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("workspace root info=%v err=%v", info, err)
	}
	for _, name := range []string{"reference", "worker"} {
		destination := filepath.Join(workspaces, name)
		if err := copyGNFProjectInputs(context.Background(), makeGNFSourceTree(t, unityVersionForTest), destination); err != nil {
			t.Fatalf("copy %s: %v", name, err)
		}
	}
}

func TestPrepareGNFWorkspacesRootRejectsPreexistingPathWithoutRemovingIt(t *testing.T) {
	artifactRoot := t.TempDir()
	workspaces := filepath.Join(artifactRoot, "workspaces")
	if err := os.Mkdir(workspaces, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(workspaces, "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("owned elsewhere"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareGNFWorkspacesRoot(artifactRoot); err == nil {
		t.Fatal("pre-existing workspace root should be rejected")
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "owned elsewhere" {
		t.Fatalf("pre-existing workspace was modified: data=%q err=%v", data, err)
	}
}

func TestCopyGNFProjectInputsRejectsMissingParent(t *testing.T) {
	source := makeGNFSourceTree(t, unityVersionForTest)
	destination := filepath.Join(t.TempDir(), "missing", "reference")
	err := copyGNFProjectInputs(context.Background(), source, destination)
	if ErrorCode(err) != CodeInvalidConfiguration {
		t.Fatalf("err=%v", err)
	}
	if _, statErr := os.Lstat(filepath.Dir(destination)); !os.IsNotExist(statErr) {
		t.Fatalf("missing parent was unexpectedly created: %v", statErr)
	}
}

func TestValidateGNFConfigRejectsDirtySource(t *testing.T) {
	root := makeGNFGitProject(t, unityVersionForTest)
	if err := os.WriteFile(filepath.Join(root, "Assets", "dirty.txt"), []byte("dirty"), 0600); err != nil {
		t.Fatal(err)
	}
	_, _, _, _, err := validateGNFConfig(context.Background(), makeGNFConfig(t, root))
	if ErrorCode(err) != CodeGNFSourceDirty {
		t.Fatalf("err=%v", err)
	}
}

func TestValidateGNFConfigRejectsUnityVersionMismatch(t *testing.T) {
	root := makeGNFGitProject(t, "6000.3.7f1")
	_, _, _, _, err := validateGNFConfig(context.Background(), makeGNFConfig(t, root))
	if ErrorCode(err) != CodeUnityVersionMismatch {
		t.Fatalf("err=%v", err)
	}
}

func TestValidateGNFConfigCapturesCleanRevisionAndBranch(t *testing.T) {
	root := makeGNFGitProject(t, unityVersionForTest)
	_, _, source, selection, err := validateGNFConfig(context.Background(), makeGNFConfig(t, root))
	if err != nil {
		t.Fatal(err)
	}
	if source.Revision == "" || source.Branch != "main" || source.GitStatus != "" || source.FixtureGitStatus != "" {
		t.Fatalf("source=%+v", source)
	}
	if len(selection.EditMode) != 1 || len(selection.PlayMode) != 1 {
		t.Fatalf("selection=%+v", selection)
	}
}

const unityVersionForTest = "6000.3.8f1"

func makeGNFConfig(t *testing.T, project string) GNFUnityConfig {
	t.Helper()
	editor := filepath.Join(t.TempDir(), "Unity.exe")
	if err := os.WriteFile(editor, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	return GNFUnityConfig{
		Pool:            Config{Root: filepath.Join(t.TempDir(), "pool")},
		UnityEditorPath: editor,
		ProjectPath:     project,
		ArtifactRoot:    filepath.Join(t.TempDir(), "artifacts"),
		TestTimeout:     time.Minute,
	}
}

func makeGNFGitProject(t *testing.T, version string) string {
	t.Helper()
	root := makeGNFSourceTree(t, version)
	runTestGit(t, root, "init", "-b", "main")
	runTestGit(t, root, "add", ".")
	runTestGit(t, root, "-c", "user.name=TestPlay", "-c", "user.email=testplay@example.invalid", "commit", "-m", "fixture")
	return root
}

func makeGNFSourceTree(t *testing.T, version string) string {
	t.Helper()
	root := t.TempDir()
	for _, directory := range []string{
		filepath.Join(root, "Assets", "Tests", "EditMode"),
		filepath.Join(root, "Assets", "Tests", "PlayMode"),
		filepath.Join(root, "Packages"),
		filepath.Join(root, "ProjectSettings"),
	} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join(root, "Assets", "Tests", "EditMode", "WallPropValidatorTests.cs"): "namespace GNF.DungeonGen.Tests\n{\npublic class WallPropValidatorTests { [Test] public void NullPrefab_Error() {} }\n}",
		filepath.Join(root, "Assets", "Tests", "PlayMode", "DOOR_CONSENSUS_Tests.cs"):   `public class DOOR_CONSENSUS_Tests { [Test] public void Proximity_CountsNearestExitWithinRadius() {} }`,
		filepath.Join(root, "Packages", "manifest.json"):                                `{}`,
		filepath.Join(root, "Packages", "packages-lock.json"):                           `{}`,
		filepath.Join(root, "ProjectSettings", "ProjectVersion.txt"):                    "m_EditorVersion: " + version + "\n",
	}
	for path, data := range files {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func runTestGit(t *testing.T, directory string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-c", "safe.directory=" + filepath.ToSlash(directory), "-C", directory}, arguments...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", arguments, err, output)
	}
}
