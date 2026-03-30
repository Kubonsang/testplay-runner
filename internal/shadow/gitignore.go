package shadow

import (
	"bufio"
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

	f, err := os.OpenFile(path, os.O_RDONLY|os.O_CREATE, 0644)
	if err != nil {
		return err
	}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == strings.TrimSpace(entry) {
			_ = f.Close()
			return nil
		}
	}
	_ = f.Close()
	if err := scanner.Err(); err != nil {
		return err
	}

	out, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0644)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = fmt.Fprintf(out, "\n%s\n", strings.TrimSpace(entry))
	return err
}
