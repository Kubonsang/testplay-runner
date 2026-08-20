package vhdxworkspace

import (
	"path/filepath"
	"testing"
	"time"
)

func TestServiceConfigParentTTLIsOptionalAndPropagated(t *testing.T) {
	root := t.TempDir()
	base := ServiceConfig{
		SchemaVersion:     ServiceConfigSchemaVersion,
		StoreRoot:         filepath.Join(root, "store"),
		WorkspaceRoot:     filepath.Join(root, "workspaces"),
		UserSID:           "S-1-5-21-test",
		QuotaBytes:        DefaultQuotaBytes,
		HostFloorBytes:    DefaultHostFloor,
		ChildReserveBytes: DefaultChildReserve,
		PipeName:          `\\.\pipe\testplay-test`,
	}
	path := filepath.Join(root, "default.json")
	if err := SaveServiceConfig(path, base); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadServiceConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ParentTTLSeconds != 0 || loaded.BrokerConfig().ParentTTL != 0 {
		t.Fatalf("default ttl=%d broker=%s", loaded.ParentTTLSeconds, loaded.BrokerConfig().ParentTTL)
	}

	base.ParentTTLSeconds = 7
	path = filepath.Join(root, "explicit.json")
	if err := SaveServiceConfig(path, base); err != nil {
		t.Fatal(err)
	}
	loaded, err = LoadServiceConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ParentTTLSeconds != 7 || loaded.BrokerConfig().ParentTTL != 7*time.Second {
		t.Fatalf("explicit ttl=%d broker=%s", loaded.ParentTTLSeconds, loaded.BrokerConfig().ParentTTL)
	}
}

func TestServiceConfigRejectsNegativeParentTTL(t *testing.T) {
	config := ServiceConfig{
		SchemaVersion:     ServiceConfigSchemaVersion,
		StoreRoot:         filepath.Join(t.TempDir(), "store"),
		WorkspaceRoot:     filepath.Join(t.TempDir(), "workspaces"),
		UserSID:           "S-1-5-21-test",
		QuotaBytes:        DefaultQuotaBytes,
		HostFloorBytes:    DefaultHostFloor,
		ChildReserveBytes: DefaultChildReserve,
		ParentTTLSeconds:  -1,
		PipeName:          `\\.\pipe\testplay-test`,
	}
	if err := config.Validate(); err == nil {
		t.Fatal("negative parent TTL accepted")
	}
}
