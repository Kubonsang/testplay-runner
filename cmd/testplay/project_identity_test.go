package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Kubonsang/testplay-runner/internal/config"
)

func TestSameProjectPath_ProvesEquivalentSpellings(t *testing.T) {
	project := t.TempDir()
	same, known := sameProjectPath(project, filepath.Join(project, "."))
	if !known || !same {
		t.Fatalf("dot alias comparison = same:%v known:%v; want true, true", same, known)
	}

	if runtime.GOOS == "windows" {
		upper := strings.ToUpper(project)
		same, known = sameProjectPath(project, upper)
		if !known || !same {
			t.Fatalf("case alias comparison = same:%v known:%v; want true, true", same, known)
		}
	}
}

func TestSameProjectPath_ResolvesFilesystemAlias(t *testing.T) {
	project := t.TempDir()
	alias := filepath.Join(t.TempDir(), "project-alias")
	if err := os.Symlink(project, alias); err != nil {
		t.Skipf("directory symlink unavailable: %v", err)
	}

	same, known := sameProjectPath(project, alias)
	if !known || !same {
		t.Fatalf("alias comparison = same:%v known:%v; want true, true", same, known)
	}
}

func TestSharedProjectRoles_UnknownIdentityForcesIsolation(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing-project")
	shared := sharedProjectRoles(map[string]*config.Config{
		"unknown": {ProjectPath: missing},
	})
	if !shared["unknown"] {
		t.Fatal("UNKNOWN project identity must force shadow isolation")
	}
}

func TestSharedProjectRoles_EquivalentPathsAreShared(t *testing.T) {
	project := t.TempDir()
	shared := sharedProjectRoles(map[string]*config.Config{
		"host":   {ProjectPath: project},
		"client": {ProjectPath: filepath.Join(project, ".")},
		"solo":   {ProjectPath: t.TempDir()},
	})
	if !shared["host"] || !shared["client"] {
		t.Fatalf("equivalent project paths must both be shared: %+v", shared)
	}
	if shared["solo"] {
		t.Fatalf("distinct known project must remain unshared: %+v", shared)
	}
}
