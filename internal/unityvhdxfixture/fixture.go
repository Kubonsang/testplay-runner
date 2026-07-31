package unityvhdxfixture

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/shadow"
)

const (
	UnityEditorEnv     = "TESTPLAY_UNITY_EDITOR_PATH"
	MarkerEnv          = "TESTPLAY_UNITY_FIXTURE_MARKER"
	TargetUnityVersion = "6000.3.8f1"
)

var forbiddenFixtureDirectories = []string{"Library", "Temp", "Logs", "UserSettings", "obj"}

func FixtureVersion(projectRoot string) (string, error) {
	path := filepath.Join(projectRoot, "ProjectSettings", "ProjectVersion.txt")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fixtureError(CodeUnityVersionMismatch, "read-project-version", path, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), ":")
		if found && key == "m_EditorVersion" {
			version := strings.TrimSpace(value)
			if version == "" {
				break
			}
			return version, nil
		}
	}
	return "", fixtureError(CodeUnityVersionMismatch, "parse-project-version", path, fmt.Errorf("m_EditorVersion is missing"))
}

func ValidateFixtureSource(root string) error {
	if !filepath.IsAbs(root) {
		return fixtureError(CodeInvalidFixtureRoot, "validate-fixture-source", root, fmt.Errorf("absolute path required"))
	}
	for _, requiredPath := range []string{filepath.Join(root, "Assets"), filepath.Join(root, "Packages", "manifest.json"), filepath.Join(root, "Packages", "packages-lock.json"), filepath.Join(root, "ProjectSettings", "ProjectVersion.txt")} {
		if _, err := os.Stat(requiredPath); err != nil {
			return fixtureError(CodeInvalidFixtureRoot, "validate-fixture-source", requiredPath, err)
		}
	}
	for _, name := range forbiddenFixtureDirectories {
		path := filepath.Join(root, name)
		if _, err := os.Lstat(path); err == nil {
			return fixtureError(CodeInvalidFixtureRoot, "validate-forbidden-directory", path, fmt.Errorf("fixture must not contain %s", name))
		} else if !os.IsNotExist(err) {
			return fixtureError(CodeInvalidFixtureRoot, "stat-forbidden-directory", path, err)
		}
	}
	return nil
}

func CopyFixtureProject(ctx context.Context, source, destination string) (int64, error) {
	if err := ValidateFixtureSource(source); err != nil {
		return 0, err
	}
	if _, err := os.Lstat(destination); err == nil {
		return 0, fixtureError(CodeInvalidFixtureRoot, "copy-fixture", destination, fmt.Errorf("destination already exists"))
	} else if !os.IsNotExist(err) {
		return 0, err
	}
	started := time.Now()
	if _, err := shadow.CopyDirParallelWithStats(ctx, source, destination, 0); err != nil {
		_ = os.RemoveAll(destination)
		return time.Since(started).Milliseconds(), fixtureError(CodeInvalidFixtureRoot, "copy-fixture", destination, err)
	}
	return time.Since(started).Milliseconds(), nil
}
