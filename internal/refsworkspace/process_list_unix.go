//go:build !windows

package refsworkspace

func namedRunningProcesses([]string) ([]string, error) {
	return nil, nil
}
