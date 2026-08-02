package gnfvhdxbenchmark

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Kubonsang/testplay-runner/internal/mountedcopy"
)

func ValidateWarmLibrary(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return benchmarkError(CodeWarmLibraryInvalid, "inspect-library", path, err)
	}
	reparse, err := mountedcopy.IsReparsePoint(path)
	if err != nil {
		return benchmarkError(CodeWarmLibraryInvalid, "inspect-library", path, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || reparse {
		return benchmarkError(CodeWarmLibraryInvalid, "inspect-library", path, fmt.Errorf("ordinary non-reparse directory required"))
	}
	for _, file := range []string{"SourceAssetDB", filepath.Join("ScriptAssemblies", SelectionAssembly)} {
		candidate := filepath.Join(path, file)
		entry, err := os.Lstat(candidate)
		if err != nil {
			return benchmarkError(CodeWarmLibraryInvalid, "inspect-sentinel", candidate, err)
		}
		reparse, err := mountedcopy.IsReparsePoint(candidate)
		if err != nil || !entry.Mode().IsRegular() || entry.Mode()&os.ModeSymlink != 0 || reparse || entry.Size() == 0 {
			return benchmarkError(CodeWarmLibraryInvalid, "validate-sentinel", candidate, fmt.Errorf("readable nonempty regular file required; reparse=%t error=%v", reparse, err))
		}
		fileHandle, err := os.Open(candidate)
		if err != nil {
			return benchmarkError(CodeWarmLibraryInvalid, "open-sentinel", candidate, err)
		}
		var first [1]byte
		_, readErr := fileHandle.Read(first[:])
		closeErr := fileHandle.Close()
		if readErr != nil || closeErr != nil {
			return benchmarkError(CodeWarmLibraryInvalid, "read-sentinel", candidate, fmt.Errorf("read=%v close=%v", readErr, closeErr))
		}
	}
	return nil
}
