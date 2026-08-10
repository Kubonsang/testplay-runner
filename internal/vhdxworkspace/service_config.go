package vhdxworkspace

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const ServiceConfigSchemaVersion = 1

type ServiceConfig struct {
	SchemaVersion     int    `json:"schemaVersion"`
	StoreRoot         string `json:"storeRoot"`
	WorkspaceRoot     string `json:"workspaceRoot"`
	UserSID           string `json:"userSid"`
	QuotaBytes        int64  `json:"quotaBytes"`
	HostFloorBytes    int64  `json:"hostFloorBytes"`
	ChildReserveBytes int64  `json:"childReserveBytes"`
	PipeName          string `json:"pipeName"`
}

func (config ServiceConfig) Validate() error {
	if config.SchemaVersion != ServiceConfigSchemaVersion || !filepath.IsAbs(config.StoreRoot) || !filepath.IsAbs(config.WorkspaceRoot) || config.UserSID == "" {
		return ErrInvalidInput
	}
	if config.QuotaBytes <= 0 || config.HostFloorBytes < 0 || config.ChildReserveBytes <= 0 {
		return ErrInvalidInput
	}
	return nil
}

func LoadServiceConfig(path string) (ServiceConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ServiceConfig{}, err
	}
	var config ServiceConfig
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return ServiceConfig{}, err
	}
	if err := config.Validate(); err != nil {
		return ServiceConfig{}, fmt.Errorf("service config: %w", err)
	}
	return config, nil
}

func SaveServiceConfig(path string, config ServiceConfig) error {
	if err := config.Validate(); err != nil {
		return err
	}
	return writeJSONDurable(path, config)
}

func (config ServiceConfig) BrokerConfig() BrokerConfig {
	return BrokerConfig{StoreRoot: config.StoreRoot, WorkspaceRoot: config.WorkspaceRoot, UserSID: config.UserSID, QuotaBytes: config.QuotaBytes, HostFloorBytes: config.HostFloorBytes, ChildReserveBytes: config.ChildReserveBytes}
}
