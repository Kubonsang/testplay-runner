package unity

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/fastplay/runner/internal/history"
)

// compileErrorRe matches Unity C# compile error lines like:
//
//	Assets/Foo.cs(42,10): error CS0246: The type 'Bar' could not be found
var compileErrorRe = regexp.MustCompile(`(?m)^(.+\.cs)\((\d+),(\d+)\): error (CS\d+: .+)$`)

// ParseCompileErrors extracts compile errors from Unity's stderr output.
func ParseCompileErrors(stderr []byte) []history.CompileError {
	return ParseCompileErrorsWithProject(stderr, "")
}

// ParseCompileErrorsWithProject extracts compile errors and resolves absolute paths.
func ParseCompileErrorsWithProject(stderr []byte, projectPath string) []history.CompileError {
	matches := compileErrorRe.FindAllSubmatch(stderr, -1)
	if len(matches) == 0 {
		return []history.CompileError{}
	}

	errs := make([]history.CompileError, 0, len(matches))
	for _, m := range matches {
		file := strings.TrimSpace(string(m[1]))
		line, _ := strconv.Atoi(string(m[2]))
		col, _ := strconv.Atoi(string(m[3]))
		msg := strings.TrimSpace(string(m[4]))

		absPath := file
		if projectPath != "" {
			absPath = filepath.Join(projectPath, filepath.FromSlash(file))
		}

		errs = append(errs, history.CompileError{
			File:         file,
			AbsolutePath: absPath,
			Line:         line,
			Column:       col,
			Message:      msg,
		})
	}
	return errs
}
