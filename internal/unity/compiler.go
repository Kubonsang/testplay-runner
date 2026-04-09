package unity

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Kubonsang/testplay-runner/internal/history"
)

// compileErrorRe matches Unity C# compile error lines like:
//
//	Assets/Foo.cs(42,10): error CS0246: The type 'Bar' could not be found
var compileErrorRe = regexp.MustCompile(`(?m)^(.+\.cs)\((\d+),(\d+)\): error (CS\d+: .+)$`)

// buildFailurePatterns matches Unity stderr output for license and build-target failures.
// These produce no results XML and no C# compile errors, so they must be detected
// separately to avoid misclassifying them as compile failures (exit 2).
var buildFailurePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)no valid unity license`),
	regexp.MustCompile(`(?i)failed to acquire.*license`),
	regexp.MustCompile(`(?i)license:.*no valid`),
	regexp.MustCompile(`(?i)is not (installed|supported)`), // build target
	regexp.MustCompile(`(?i)module.*\bmissing\b`),
}

// ParseBuildFailure reports whether stderr looks like a Unity license or
// build-target failure rather than a C# compile error.
func ParseBuildFailure(stderr []byte) bool {
	for _, re := range buildFailurePatterns {
		if re.Match(stderr) {
			return true
		}
	}
	return false
}

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
			absPath = filepath.ToSlash(filepath.Join(projectPath, filepath.FromSlash(file)))
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
