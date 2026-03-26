package main

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Kubonsang/testplay-runner/internal/config"
)

type listDeps struct {
	projectPath string
}

func runList(w io.Writer, deps listDeps) int {
	tests := make([]string, 0)

	err := filepath.WalkDir(deps.projectPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable dirs
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".cs") {
			return nil
		}

		found, err := scanCSharpTestFile(path)
		if err != nil {
			return nil // skip files we can't read
		}
		tests = append(tests, found...)
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, map[string]any{"tests": tests})
			return 0
		}
		writeJSON(w, map[string]any{"tests": tests, "error": err.Error()})
		return 1
	}

	writeJSON(w, map[string]any{"tests": tests})
	return 0
}

// scanCSharpTestFile returns method names annotated with [Test] in the file.
// It uses a simple line-by-line scan: if a line contains "[Test]" and the next
// non-empty line looks like a method signature, extract the method name.
func scanCSharpTestFile(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var results []string
	var className string
	var nextIsTest bool

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Extract class name
		if strings.HasPrefix(line, "public class ") || strings.HasPrefix(line, "class ") {
			parts := strings.Fields(line)
			for i, p := range parts {
				if p == "class" && i+1 < len(parts) {
					className = strings.TrimSuffix(parts[i+1], "{")
					break
				}
			}
		}

		if strings.Contains(line, "[Test]") || strings.Contains(line, "[UnityTest]") {
			nextIsTest = true
			continue
		}

		if nextIsTest && line != "" {
			nextIsTest = false
			// Extract method name from line like: "public void TestAdd() {}"
			methodName := extractMethodName(line)
			if methodName != "" {
				name := methodName
				if className != "" {
					name = className + "." + methodName
				}
				results = append(results, name)
			}
		}
	}
	return results, scanner.Err()
}

// extractMethodName extracts the method name from a C# method signature line.
func extractMethodName(line string) string {
	// Look for pattern: <modifiers> <returnType> <methodName>(
	idx := strings.Index(line, "(")
	if idx == -1 {
		return ""
	}
	before := strings.TrimSpace(line[:idx])
	parts := strings.Fields(before)
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1] // last word before "(" is the method name
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List candidate test names from source (static scan, may be incomplete)",
	RunE: func(cmd *cobra.Command, args []string) error {
		deps := listDeps{projectPath: "."}
		// Try to load config to get project path
		cfg, err := config.Load("fastplay.json")
		if err == nil {
			_ = cfg.Validate(false)
			deps.projectPath = cfg.ProjectPath
		}
		code := runList(cmd.OutOrStdout(), deps)
		os.Exit(code)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(listCmd)
}
