//go:build !windows

package refsworkspace

import "fmt"

func DefaultConfig() (Config, error) {
	return Config{}, fmt.Errorf("Managed ReFS Library Pool is available only on Windows")
}
