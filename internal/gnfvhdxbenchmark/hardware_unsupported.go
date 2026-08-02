//go:build !windows

package gnfvhdxbenchmark

import (
	"context"
	"fmt"
	"runtime"
)

func runHardware(context.Context, HardwareConfig) (Summary, error) {
	return Summary{}, benchmarkError(CodeUnsupportedPlatform, "run-hardware-benchmark", runtime.GOOS, fmt.Errorf("Windows is required"))
}
