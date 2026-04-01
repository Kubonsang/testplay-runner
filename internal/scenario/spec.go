package scenario

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrScenarioInvalid is returned when a scenario file fails validation.
var ErrScenarioInvalid = errors.New("scenario file is invalid")

// InstanceSpec describes a single instance to run in the scenario.
type InstanceSpec struct {
	Role   string `json:"role"`
	Config string `json:"config"` // path to testplay.json, relative to scenario file or absolute
}

// ScenarioFile is the parsed representation of a scenario JSON file.
type ScenarioFile struct {
	SchemaVersion string         `json:"schema_version"`
	Instances     []InstanceSpec `json:"instances"`
	dir           string         // directory containing the scenario file (for relative path resolution)
}

// ConfigPath resolves inst.Config relative to the scenario file's directory.
// If inst.Config is already absolute, it is returned as-is.
func (f *ScenarioFile) ConfigPath(inst InstanceSpec) string {
	if filepath.IsAbs(inst.Config) {
		return inst.Config
	}
	return filepath.Join(f.dir, inst.Config)
}

// Load reads, parses, and validates a scenario file from path.
func Load(path string) (*ScenarioFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s not found", ErrScenarioInvalid, path)
		}
		return nil, fmt.Errorf("%w: %v", ErrScenarioInvalid, err)
	}

	var sf ScenarioFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrScenarioInvalid, err)
	}

	if sf.SchemaVersion == "" {
		return nil, fmt.Errorf("%w: schema_version is required", ErrScenarioInvalid)
	}
	if len(sf.Instances) == 0 {
		return nil, fmt.Errorf("%w: instances must not be empty", ErrScenarioInvalid)
	}
	for i, inst := range sf.Instances {
		if inst.Role == "" {
			return nil, fmt.Errorf("%w: instances[%d].role is required", ErrScenarioInvalid, i)
		}
		if inst.Config == "" {
			return nil, fmt.Errorf("%w: instances[%d].config is required", ErrScenarioInvalid, i)
		}
	}

	sf.dir = filepath.Dir(filepath.Clean(path))
	return &sf, nil
}
