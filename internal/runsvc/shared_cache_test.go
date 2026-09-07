package runsvc_test

import (
	"context"
	"github.com/Kubonsang/testplay-runner/internal/config"
	"github.com/Kubonsang/testplay-runner/internal/contentcache"
	"github.com/Kubonsang/testplay-runner/internal/runsvc"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServiceSharedCacheWritesRestoresAndPreservesLocalCache(t *testing.T) {
	cfg, project := imageConfig(t)
	cfg.UnityPath = filepath.Join(t.TempDir(), "unity.exe")
	if err := os.WriteFile(cfg.UnityPath, []byte("test executable"), 0600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "store")
	cfg.Workspace = &config.WorkspaceConfig{CacheMode: "shared-content", CacheRoot: root}
	old := filepath.Join(project, ".testplay", "cache", "Library")
	if err := os.MkdirAll(old, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(old, "preserved")
	if err := os.WriteFile(sentinel, []byte("legacy cache"), 0600); err != nil {
		t.Fatal(err)
	}
	runner := &imageTestRunner{resultsXML: mustReadFixture(t, "../../internal/parser/testdata/passing.xml")}
	svc := imageService(cfg, project, runner)
	for i := 0; i < 2; i++ {
		if i == 1 {
			if err := os.WriteFile(filepath.Join(root, "blobs", strings.Repeat("f", 64)), []byte("unreferenced fixture"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		response, err := svc.Run(context.Background(), runsvc.Request{Config: cfg, ForceShadow: true})
		if err != nil || response.ExitCode != 0 || len(response.Warnings) > 0 {
			t.Fatalf("run %d: %+v %v", i, response, err)
		}
	}
	usage, err := contentcache.New(root).Inspect(context.Background(), false)
	if err != nil || usage.ManifestCount != 1 || usage.UniqueBytes == 0 || usage.ReclaimableBytes != 0 {
		t.Fatalf("shared cache not populated: %+v %v", usage, err)
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "legacy cache" {
		t.Fatal("legacy cache deleted")
	}
}
