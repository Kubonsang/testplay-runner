//go:build windows

package refsworkspace

func platformPathIsReparse(path string) (bool, error) {
	return pathIsReparsePoint(path)
}
