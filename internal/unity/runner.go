package unity

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// waitDelayAfterKill is how long cmd.Run waits for the process to exit
// after cancellation before giving up. Used by both Unix and Windows
// implementations of setSysProcAttr.
const waitDelayAfterKill = 5 * time.Second

// Runner abstracts the Unity subprocess, allowing tests to inject a fake.
//
// Current scope: single Unity process per invocation. Each call to Run starts
// one Unity batch-mode process and waits for it to exit. There is no inter-process
// communication, log streaming, or multi-process orchestration.
//
// Future work: network harness / NGO orchestration will require running multiple
// processes concurrently (e.g. server + client Unity instances). That will need a
// different abstraction — Runner is intentionally kept minimal until then.
type Runner interface {
	// Run executes Unity with the given args, streaming stdout and stderr to the
	// provided writers. Either writer may be nil (output is discarded).
	Run(ctx context.Context, args []string, stdout, stderr io.Writer) (exitCode int, err error)
}

// ProcessRunner is the real implementation backed by exec.Cmd.
type ProcessRunner struct {
	UnityPath string
	Env       map[string]string // extra env vars merged with os.Environ(); nil = inherit
}

// Run executes the Unity binary with the provided args, streaming output to
// stdout and stderr. A nil writer discards the corresponding output.
func (r *ProcessRunner) Run(ctx context.Context, args []string, stdout, stderr io.Writer) (int, error) {
	cmd := exec.CommandContext(ctx, r.UnityPath, args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if len(r.Env) > 0 {
		cmd.Env = MergeEnv(os.Environ(), r.Env)
	}
	setSysProcAttr(cmd)
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), nil
		}
		return -1, err
	}
	return 0, nil
}

// MergeEnv returns base with extra vars added or overridden.
// Keys in extra replace matching keys in base (case-sensitive).
// If extra is nil or empty, base is returned as-is.
func MergeEnv(base []string, extra map[string]string) []string {
	if len(extra) == 0 {
		return base
	}
	env := make([]string, 0, len(base)+len(extra))
	for _, e := range base {
		k, _, _ := strings.Cut(e, "=")
		if _, override := extra[k]; !override {
			env = append(env, e)
		}
	}
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}

// BuildCompileArgs constructs the Unity CLI arguments for a compile-only run
// (no -runTests, exits after compilation via -quit). Used by the two-phase
// executor to enforce a separate compile timeout.
func BuildCompileArgs(projectPath string) []string {
	return []string{
		"-batchmode",
		"-nographics",
		"-projectPath", projectPath,
		"-quit",
	}
}

// RunOptions configures a Unity test run.
type RunOptions struct {
	Filter          string
	Category        string
	ResultsFilePath string
	TestPlatform    string // "edit_mode" | "play_mode"; defaults to EditMode if empty
}

// BuildRunArgs constructs the Unity CLI arguments for a test run.
func BuildRunArgs(projectPath string, opts *RunOptions) []string {
	platform := "EditMode"
	if opts.TestPlatform == "play_mode" {
		platform = "PlayMode"
	}

	args := []string{
		"-batchmode",
		"-nographics",
		"-runTests",
		"-testPlatform", platform,
		"-projectPath", projectPath,
	}

	if opts.ResultsFilePath != "" {
		args = append(args, "-testResults", opts.ResultsFilePath)
	}
	if opts.Filter != "" {
		args = append(args, "-testFilter", opts.Filter)
	}
	if opts.Category != "" {
		args = append(args, "-testCategory", opts.Category)
	}

	return args
}
