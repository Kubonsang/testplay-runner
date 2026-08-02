package libraryimage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestComputeKey_Deterministic(t *testing.T) {
	project := makeKeyProject(t)

	first, err := ComputeKey(project, "/Unity/Editor")
	if err != nil {
		t.Fatalf("ComputeKey: %v", err)
	}
	second, err := ComputeKey(project, "/Unity/Editor")
	if err != nil {
		t.Fatalf("ComputeKey second call: %v", err)
	}
	if first != second {
		t.Fatalf("keys differ:\nfirst:  %+v\nsecond: %+v", first, second)
	}
	if len(first.Digest) != 64 {
		t.Fatalf("Digest length = %d, want 64", len(first.Digest))
	}
}

func TestComputeKey_InvalidatesRelevantInputs(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, project string)
	}{
		{
			name: "Unity version",
			mutate: func(t *testing.T, project string) {
				writeFile(t, filepath.Join(project, "ProjectSettings", "ProjectVersion.txt"),
					"m_EditorVersion: 6000.3.9f1\n")
			},
		},
		{
			name: "manifest",
			mutate: func(t *testing.T, project string) {
				writeFile(t, filepath.Join(project, "Packages", "manifest.json"),
					`{"dependencies":{"com.unity.test-framework":"1.4.6"}}`)
			},
		},
		{
			name: "packages lock",
			mutate: func(t *testing.T, project string) {
				writeFile(t, filepath.Join(project, "Packages", "packages-lock.json"),
					`{"dependencies":{"com.unity.test-framework":{"version":"1.4.6"}}}`)
			},
		},
		{
			name: "ProjectSettings",
			mutate: func(t *testing.T, project string) {
				writeFile(t, filepath.Join(project, "ProjectSettings", "ProjectSettings.asset"),
					"PlayerSettings:\n  scriptingBackend: 1\n")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			project := makeKeyProject(t)
			before, err := ComputeKey(project, "/Unity/Editor")
			if err != nil {
				t.Fatalf("ComputeKey before mutation: %v", err)
			}
			test.mutate(t, project)
			after, err := ComputeKey(project, "/Unity/Editor")
			if err != nil {
				t.Fatalf("ComputeKey after mutation: %v", err)
			}
			if before.Digest == after.Digest {
				t.Fatalf("Digest did not change after %s mutation", test.name)
			}
		})
	}
}

func TestComputeKey_MissingPackagesLockIsExplicit(t *testing.T) {
	project := makeKeyProject(t)
	if err := os.Remove(filepath.Join(project, "Packages", "packages-lock.json")); err != nil {
		t.Fatal(err)
	}
	key, err := ComputeKey(project, "/Unity/Editor")
	if err != nil {
		t.Fatalf("ComputeKey: %v", err)
	}
	if key.PackagesLockSHA256 != "missing" {
		t.Fatalf("PackagesLockSHA256 = %q, want missing", key.PackagesLockSHA256)
	}
}

func makeKeyProject(t *testing.T) string {
	t.Helper()
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, "ProjectSettings"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(project, "Packages"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(project, "ProjectSettings", "ProjectVersion.txt"),
		"m_EditorVersion: 6000.3.8f1\n")
	writeFile(t, filepath.Join(project, "ProjectSettings", "ProjectSettings.asset"),
		"PlayerSettings:\n  scriptingBackend: 0\n")
	writeFile(t, filepath.Join(project, "Packages", "manifest.json"),
		`{"dependencies":{"com.unity.test-framework":"1.4.5"}}`)
	writeFile(t, filepath.Join(project, "Packages", "packages-lock.json"),
		`{"dependencies":{"com.unity.test-framework":{"version":"1.4.5"}}}`)
	return project
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}
}
