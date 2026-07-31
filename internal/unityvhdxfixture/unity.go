package unityvhdxfixture

import (
	"bytes"
	"context"
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
	runner := &unity.ProcessRunner{UnityPath: e.EditorPath, Env: map[string]string{MarkerEnv: e.Marker}}
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
	args := unity.BuildRunArgs(projectPath, &unity.RunOptions{ResultsFilePath: resultsPath, TestPlatform: platform})
	args = append(args, "-logFile", logPath)
	runner := &unity.ProcessRunner{UnityPath: e.EditorPath, Env: map[string]string{MarkerEnv: e.Marker}}
	started := time.Now()
	var output synchronizedBuffer
	exitCode, runErr := runner.Run(ctx, args, &output, &output)
	wall := time.Since(started).Milliseconds()
	ensureUnityLog(logPath, output.String())
	if runErr != nil {
		return PlatformResult{}, fixtureError(CodeUnityRunFailed, "run-tests", projectPath, runErr)
	}
	if exitCode != 0 {
		return PlatformResult{}, classifyUnityFailure("run-tests", projectPath, logPath, exitCode)
	}
	result, err := ParsePlatformResult(platform, exitCode, resultsPath, logPath, wall)
	if err != nil {
		return PlatformResult{}, err
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
	text := strings.ToLower(string(data))
	code := CodeUnityRunFailed
	if strings.Contains(text, "license") && (strings.Contains(text, "failed") || strings.Contains(text, "not found") || strings.Contains(text, "not valid")) {
		code = CodeUnityLicenseFailed
	} else if strings.Contains(text, "package") && (strings.Contains(text, "resolution failed") || strings.Contains(text, "failed to resolve") || strings.Contains(text, "cannot resolve")) {
		code = CodeUnityPackageResolutionFailed
	}
	return fixtureError(code, operation, projectPath, fmt.Errorf("Unity exited with code %d; log=%s", exitCode, logPath))
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
