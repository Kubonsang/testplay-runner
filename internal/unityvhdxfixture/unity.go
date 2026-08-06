package unityvhdxfixture

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/unity"
)

type UnityExecutor struct {
	EditorPath string
	Version    string
	Marker     string
	Filter     string
	OnStart    func(pid int, startedAt time.Time)
}

type synchronizedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *synchronizedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(data)
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func ResolveUnityEditor() (string, error) {
	path := strings.TrimSpace(os.Getenv(UnityEditorEnv))
	if path == "" {
		return "", fixtureError(CodeUnityEditorNotFound, "resolve-editor", UnityEditorEnv, fmt.Errorf("environment variable is not set"))
	}
	if !filepath.IsAbs(path) {
		return "", fixtureError(CodeUnityEditorNotFound, "resolve-editor", path, fmt.Errorf("absolute path required"))
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		if err == nil {
			err = fmt.Errorf("editor path is not a regular file")
		}
		return "", fixtureError(CodeUnityEditorNotFound, "resolve-editor", path, err)
	}
	return filepath.Clean(path), nil
}

func (e UnityExecutor) ValidateVersion(ctx context.Context, fixtureVersion string) error {
	if fixtureVersion == "" || e.Version == "" || fixtureVersion != e.Version {
		return fixtureError(CodeUnityVersionMismatch, "validate-configured-version", e.EditorPath, fmt.Errorf("fixture=%q expected=%q", fixtureVersion, e.Version))
	}
	var output synchronizedBuffer
	runner := &unity.ProcessRunner{UnityPath: e.EditorPath}
	exitCode, err := runner.Run(ctx, []string{"-version"}, &output, &output)
	if err != nil {
		return fixtureError(CodeUnityEditorNotFound, "query-editor-version", e.EditorPath, err)
	}
	if exitCode != 0 {
		return fixtureError(CodeUnityEditorNotFound, "query-editor-version", e.EditorPath, fmt.Errorf("exit=%d output=%s", exitCode, strings.TrimSpace(output.String())))
	}
	installed := strings.TrimSpace(output.String())
	if !strings.Contains(installed, fixtureVersion) {
		return fixtureError(CodeUnityVersionMismatch, "query-editor-version", e.EditorPath, fmt.Errorf("fixture=%s installed=%s", fixtureVersion, installed))
	}
	return nil
}

func (e UnityExecutor) RunCompile(ctx context.Context, projectPath, logPath string) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(logPath), 0700); err != nil {
		return 0, err
	}
	args := append(unity.BuildCompileArgs(projectPath), "-logFile", logPath)
	started := time.Now()
	runner := &unity.ProcessRunner{UnityPath: e.EditorPath, Env: map[string]string{MarkerEnv: e.Marker}, OnStart: e.OnStart}
	var output synchronizedBuffer
	exitCode, runErr := runner.Run(ctx, args, &output, &output)
	wall := time.Since(started).Milliseconds()
	ensureUnityLog(logPath, output.String())
	if runErr != nil {
		return wall, fixtureError(CodeUnityRunFailed, "seed-import", projectPath, runErr)
	}
	if exitCode != 0 {
		return wall, classifyUnityFailure("seed-import", projectPath, logPath, exitCode)
	}
	return wall, nil
}

func (e UnityExecutor) RunTests(ctx context.Context, projectPath, platform, resultsPath, logPath string) (PlatformResult, error) {
	for _, path := range []string{resultsPath, logPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return PlatformResult{}, err
		}
	}
	args := unity.BuildRunArgs(projectPath, &unity.RunOptions{ResultsFilePath: resultsPath, TestPlatform: platform, Filter: e.Filter})
	args = append(args, "-logFile", logPath)
	runner := &unity.ProcessRunner{UnityPath: e.EditorPath, Env: map[string]string{MarkerEnv: e.Marker}, OnStart: e.OnStart}
	started := time.Now()
	var output synchronizedBuffer
	exitCode, runErr := runner.Run(ctx, args, &output, &output)
	wall := time.Since(started).Milliseconds()
	ensureUnityLog(logPath, output.String())
	result, resultErr := ParsePlatformResult(platform, exitCode, resultsPath, logPath, wall)
	if runErr != nil {
		return result, errors.Join(fixtureError(CodeUnityRunFailed, "run-tests", projectPath, runErr), resultErr)
	}
	if exitCode != 0 {
		return result, errors.Join(classifyUnityFailure("run-tests", projectPath, logPath, exitCode), resultErr)
	}
	if resultErr != nil {
		return PlatformResult{}, resultErr
	}
	return result, nil
}

func ensureUnityLog(path, fallback string) {
	if _, err := os.Stat(path); err == nil {
		return
	}
	_ = os.WriteFile(path, []byte(fallback), 0600)
}

func classifyUnityFailure(operation, projectPath, logPath string, exitCode int) error {
	data, _ := os.ReadFile(logPath)
	code := classifyUnityFailureCode(string(data), exitCode)
	return fixtureError(code, operation, projectPath, fmt.Errorf("Unity exited with code %d; log=%s", exitCode, logPath))
}

func classifyUnityFailureCode(logText string, exitCode int) string {
	text := strings.ToLower(logText)
	if containsAny(text, "mdb_env_open failed", "cannot open lmdb database", "sourceassetdb") {
		return CodeUnityAssetDatabaseOpenFailed
	}
	if exitCode == 0xC0000005 || containsAny(text, "crash!!!", "access violation", "a crash has been intercepted") {
		return CodeUnityNativeCrash
	}
	if strings.Contains(text, "package") && containsAny(text, "resolution failed", "failed to resolve", "cannot resolve") {
		return CodeUnityPackageResolutionFailed
	}
	if containsAny(text,
		"licensing failed to initialize",
		"license activation failed",
		"failed to activate license",
		"no valid unity editor license found",
		"unity editor license has expired",
		"license is not valid",
	) {
		return CodeUnityLicenseFailed
	}
	if containsAny(text, "library path is unavailable", "library directory is unavailable") {
		return CodeUnityLibraryPathUnavailable
	}
	return CodeUnityRunFailed
}

func containsAny(text string, patterns ...string) bool {
	for _, pattern := range patterns {
		if strings.Contains(text, pattern) {
			return true
		}
	}
	return false
}

func ObserveReimport(logPaths ...string) ReimportObservations {
	var combined strings.Builder
	for _, path := range logPaths {
		file, err := os.Open(path)
		if err != nil {
			continue
		}
		_, _ = io.Copy(&combined, file)
		_ = file.Close()
		combined.WriteByte('\n')
	}
	text := strings.ToLower(combined.String())
	return ReimportObservations{
		ScriptCompilation: observed(text, "compil"),
		PackageResolution: observed(text, "resolving packages", "package resolution"),
		AssetImport:       observed(text, "asset import", "importing assets"),
		DomainReload:      observed(text, "domain reload", "reload assemblies"),
		LibraryRebuild:    observed(text, "rebuilding library", "library rebuild"),
	}
}

func observed(text string, patterns ...string) *bool {
	value := false
	for _, pattern := range patterns {
		if strings.Contains(text, pattern) {
			value = true
			break
		}
	}
	return &value
}
