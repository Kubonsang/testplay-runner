package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Kubonsang/testplay-runner/internal/refsworkspace"
)

func main() {
	var config refsworkspace.GNFUnityConfig
	var timeout time.Duration
	flag.StringVar(&config.UnityEditorPath, "unity-editor", "", "absolute Unity Editor executable")
	flag.StringVar(&config.ProjectPath, "project", "", "absolute clean GNF Unity project")
	flag.StringVar(&config.ArtifactRoot, "artifact-root", "", "absolute artifact directory outside the VHDX")
	flag.StringVar(&config.Pool.Root, "storage-root", "", "absolute fresh host storage root")
	flag.StringVar(&config.Pool.VHDXPath, "pool-file", "", "absolute VHDX path directly under storage root")
	flag.StringVar(&config.Pool.MountRoot, "mount-root", "", "absolute private mount path directly under storage root")
	flag.Int64Var(&config.Pool.MaximumBytes, "max-bytes", refsworkspace.DefaultMaximumBytes, "dynamic VHDX maximum bytes")
	flag.Int64Var(&config.Pool.SoftBudgetBytes, "soft-budget-bytes", refsworkspace.DefaultSoftBudget, "pool soft budget bytes")
	flag.Int64Var(&config.Pool.WorkerReserveBytes, "worker-reserve-bytes", refsworkspace.DefaultReserveBytes, "single worker reserve bytes")
	flag.DurationVar(&timeout, "test-timeout", 30*time.Minute, "timeout for each Unity process")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "positional arguments are not accepted")
		os.Exit(2)
	}
	config.Pool.MinimumHostFreeBytes = refsworkspace.DefaultMinimumHostFreeBytes
	config.Pool.VHDXOverheadReserveBytes = refsworkspace.DefaultVHDXOverheadReserveBytes
	config.TestTimeout = timeout
	config.PolicyOverride = config.Pool.SoftBudgetBytes != refsworkspace.DefaultSoftBudget || config.Pool.WorkerReserveBytes != refsworkspace.DefaultReserveBytes

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	summary, err := refsworkspace.RunGNFUnity(ctx, config)
	_ = json.NewEncoder(os.Stdout).Encode(summary)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
