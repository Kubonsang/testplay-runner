package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/refsworkspace"
)

func main() {
	var config refsworkspace.GNFUnityConfig
	var timeout time.Duration
	flag.StringVar(&config.UnityEditorPath, "unity-editor", "", "absolute Unity Editor executable")
	flag.StringVar(&config.ProjectPath, "project", "", "absolute clean GNF Unity project")
	flag.StringVar(&config.LocalPackagePath, "unity-cli-connector", "", "absolute clean portable Unity CLI connector package")
	flag.StringVar(&config.ArtifactRoot, "artifact-root", "", "absolute artifact directory")
	flag.DurationVar(&timeout, "test-timeout", 30*time.Minute, "timeout for each Unity process")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "positional arguments are not accepted")
		os.Exit(2)
	}
	unusedRoot := filepath.Join(config.ArtifactRoot, "unused-storage-root")
	config.Pool = refsworkspace.Config{Root: unusedRoot, VHDXPath: filepath.Join(unusedRoot, "unused.vhdx"), MountRoot: filepath.Join(unusedRoot, "mount"), MaximumBytes: refsworkspace.DefaultMaximumBytes, SoftBudgetBytes: refsworkspace.DefaultSoftBudget, WorkerReserveBytes: refsworkspace.DefaultReserveBytes, MinimumHostFreeBytes: refsworkspace.DefaultMinimumHostFreeBytes, VHDXOverheadReserveBytes: refsworkspace.DefaultVHDXOverheadReserveBytes}
	config.TestTimeout, config.WorkerCount, config.ReferenceSmokeRuns = timeout, 1, 2
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	summary, err := refsworkspace.RunGNFUnity(ctx, config)
	_ = json.NewEncoder(os.Stdout).Encode(summary)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
