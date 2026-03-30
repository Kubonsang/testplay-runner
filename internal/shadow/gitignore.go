package shadow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnsureIgnored appends entry to <projectPath>/.gitignore if not already present.
// Creates the file if it does not exist. A failure here is non-fatal for the
// caller — the shadow workspace will still function without .gitignore coverage.
func EnsureIgnored(projectPath, entry string) error {
	path := filepath.Join(projectPath, ".gitignore")
	trimmed := strings.TrimSpace(entry)

	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	// Check if entry already present.
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == trimmed {
			return nil
		}
	}

	// Append entry.
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0644)
	if err != nil {
		return err
	}
	defer out.Close()
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		_, err = fmt.Fprintf(out, "\n%s\n", trimmed)
	} else {
		_, err = fmt.Fprintf(out, "%s\n", trimmed)
	}
	return err
}
