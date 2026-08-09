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
	var config refsworkspace.UnityParallelConfig
	var timeout time.Duration
	flag.StringVar(&config.UnityEditorPath, "unity-editor", "", "absolute Unity Editor executable")
	flag.StringVar(&config.FixturePath, "fixture", "", "absolute clean fixture source")
	flag.StringVar(&config.ArtifactRoot, "artifact-root", "", "absolute artifact directory outside the VHDX")
	flag.StringVar(&config.Pool.Root, "storage-root", "", "absolute fresh host storage root")
	flag.StringVar(&config.Pool.VHDXPath, "pool-file", "", "absolute VHDX path directly under storage root")
	flag.StringVar(&config.Pool.MountRoot, "mount-root", "", "absolute private mount path directly under storage root")
	flag.Int64Var(&config.Pool.MaximumBytes, "max-bytes", refsworkspace.DefaultMaximumBytes, "dynamic VHDX maximum bytes")
	flag.Int64Var(&config.Pool.SoftBudgetBytes, "soft-budget-bytes", refsworkspace.DefaultSoftBudget, "experimental pool soft budget bytes")
	flag.Int64Var(&config.Pool.WorkerReserveBytes, "worker-reserve-bytes", refsworkspace.DefaultReserveBytes, "per-worker reserve bytes")
	flag.Int64Var(&config.Pool.MinimumHostFreeBytes, "minimum-host-free-bytes", refsworkspace.DefaultMinimumHostFreeBytes, "base host free-space floor")
	flag.Int64Var(&config.Pool.VHDXOverheadReserveBytes, "vhdx-overhead-reserve-bytes", refsworkspace.DefaultVHDXOverheadReserveBytes, "host VHDX overhead reserve")
	flag.IntVar(&config.WorkerCount, "worker-count", 0, "experimental worker count; 2, 4, or 8 required")
	flag.BoolVar(&config.SizingOnly, "sizing-only", false, "build and measure the canonical baseline, then remove the sizing pool")
	flag.Int64Var(&config.BaselineSizingUsedBytes, "baseline-sizing-used-bytes", 0, "measured used-after-baseline bytes from a separate sizing run")
	flag.DurationVar(&timeout, "test-timeout", 20*time.Minute, "timeout for each Unity process")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "positional arguments are not accepted")
		os.Exit(2)
	}
	config.TestTimeout = timeout

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	summary, err := refsworkspace.RunUnityParallel(ctx, config)
	_ = json.NewEncoder(os.Stdout).Encode(summary)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
