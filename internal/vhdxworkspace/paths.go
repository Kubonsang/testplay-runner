package vhdxworkspace

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type Paths struct {
	Root       string
	UserRoot   string
	Parents    string
	Pending    string
	Children   string
	Leases     string
	Retained   string
	Quarantine string
	Receipts   string
}

func NewPaths(root, userSID string) (Paths, error) {
	if !filepath.IsAbs(root) || strings.TrimSpace(userSID) == "" || strings.ContainsAny(userSID, `/\\`) {
		return Paths{}, fmt.Errorf("%w: invalid root or SID", ErrInvalidInput)
	}
	clean := filepath.Clean(root)
	userRoot := filepath.Join(clean, userSID)
	return Paths{
		Root: clean, UserRoot: userRoot,
		Parents: filepath.Join(userRoot, "parents"), Pending: filepath.Join(userRoot, "pending"),
		Children: filepath.Join(userRoot, "children"), Leases: filepath.Join(userRoot, "leases"),
		Retained: filepath.Join(userRoot, "retained"), Quarantine: filepath.Join(userRoot, "quarantine"),
		Receipts: filepath.Join(userRoot, "receipts"),
	}, nil
}

func (p Paths) Parent(key string) (string, error) {
	if !digestPattern.MatchString(key) {
		return "", fmt.Errorf("%w: invalid compatibility key", ErrInvalidInput)
	}
	return filepath.Join(p.Parents, key), nil
}

func (p Paths) Child(leaseID string) (string, error) {
	if !identifierPattern.MatchString(leaseID) {
		return "", fmt.Errorf("%w: invalid lease ID", ErrInvalidInput)
	}
	return filepath.Join(p.Children, leaseID+".vhdx"), nil
}

func (p Paths) Lease(leaseID string) (string, error) {
	if !identifierPattern.MatchString(leaseID) {
		return "", fmt.Errorf("%w: invalid lease ID", ErrInvalidInput)
	}
	return filepath.Join(p.Leases, leaseID+".json"), nil
}

func (p Paths) RetainedRecord(runID string) (string, error) {
	if !identifierPattern.MatchString(runID) {
		return "", fmt.Errorf("%w: invalid run ID", ErrInvalidInput)
	}
	return filepath.Join(p.Retained, runID+".json"), nil
}
