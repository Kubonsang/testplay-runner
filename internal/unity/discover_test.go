package unity_test

import (
	"testing"

	"github.com/fastplay/runner/internal/config"
	"github.com/fastplay/runner/internal/unity"
)

func TestDiscover_UsesConfigPath(t *testing.T) {
	cfg := &config.Config{UnityPath: "/fake/unity"}
	path, err := unity.DiscoverUnityPath(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if path != "/fake/unity" {
		t.Errorf("got %q", path)
	}
}

func TestDiscover_FallsBackToEnv(t *testing.T) {
	t.Setenv("UNITY_PATH", "/env/unity")
	cfg := &config.Config{} // no UnityPath
	path, err := unity.DiscoverUnityPath(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if path != "/env/unity" {
		t.Errorf("got %q", path)
	}
}

func TestDiscover_ReturnsErrorWhenNotFound(t *testing.T) {
	t.Setenv("UNITY_PATH", "")
	cfg := &config.Config{} // no UnityPath, no env
	_, err := unity.DiscoverUnityPath(cfg)
	if err == nil {
		t.Error("expected error when no Unity path found")
	}
}
