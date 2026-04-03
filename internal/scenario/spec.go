package scenario

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrScenarioInvalid is returned when a scenario file fails validation.
var ErrScenarioInvalid = errors.New("scenario file is invalid")

// InstanceSpec describes a single instance to run in the scenario.
type InstanceSpec struct {
	Role           string            `json:"role"`
	Config         string            `json:"config"`                      // path to testplay.json, relative to scenario file or absolute
	DependsOn      string            `json:"depends_on,omitempty"`        // role this instance waits for before starting
	ReadyPhase     string            `json:"ready_phase,omitempty"`       // phase to wait for in the depended-on instance
	ReadyTimeoutMs int               `json:"ready_timeout_ms,omitempty"`  // how long to wait for the dependency (ms)
	Env            map[string]string `json:"env,omitempty"`               // extra env vars merged with os.Environ() for this instance
}

// EffectiveReadyPhase returns the phase string to wait for, defaulting to "compiling".
// "compiling" is the first phase written by runsvc.Service.Run() — immediately before
// Unity is invoked — and is the earliest observable signal that an instance has started.
func (inst InstanceSpec) EffectiveReadyPhase() string {
	if inst.ReadyPhase == "" {
		return "compiling"
	}
	return inst.ReadyPhase
}

// EffectiveReadyTimeoutMs returns the dependency wait timeout in milliseconds, defaulting to 30000.
func (inst InstanceSpec) EffectiveReadyTimeoutMs() int {
	if inst.ReadyTimeoutMs <= 0 {
		return 30000
	}
	return inst.ReadyTimeoutMs
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
	// Build role set for cross-reference validation.
	roles := make(map[string]struct{}, len(sf.Instances))
	for i, inst := range sf.Instances {
		if inst.Role == "" {
			return nil, fmt.Errorf("%w: instances[%d].role is required", ErrScenarioInvalid, i)
		}
		if inst.Config == "" {
			return nil, fmt.Errorf("%w: instances[%d].config is required", ErrScenarioInvalid, i)
		}
		if _, dup := roles[inst.Role]; dup {
			return nil, fmt.Errorf("%w: instances[%d].role %q is not unique", ErrScenarioInvalid, i, inst.Role)
		}
		roles[inst.Role] = struct{}{}
	}
	// Validate depends_on references.
	for i, inst := range sf.Instances {
		if inst.DependsOn == "" {
			continue
		}
		if _, ok := roles[inst.DependsOn]; !ok {
			return nil, fmt.Errorf("%w: instances[%d].depends_on %q references unknown role", ErrScenarioInvalid, i, inst.DependsOn)
		}
		if inst.DependsOn == inst.Role {
			return nil, fmt.Errorf("%w: instances[%d].depends_on %q cannot depend on itself", ErrScenarioInvalid, i, inst.Role)
		}
	}

	// Validate env keys.
	for i, inst := range sf.Instances {
		for k := range inst.Env {
			if k == "" {
				return nil, fmt.Errorf("%w: instances[%d].env contains empty key", ErrScenarioInvalid, i)
			}
			if strings.Contains(k, "=") {
				return nil, fmt.Errorf("%w: instances[%d].env key %q must not contain '='", ErrScenarioInvalid, i, k)
			}
		}
	}

	// Detect circular dependencies via DFS.
	// Build adjacency: role → depends_on role.
	deps := make(map[string]string, len(sf.Instances))
	for _, inst := range sf.Instances {
		if inst.DependsOn != "" {
			deps[inst.Role] = inst.DependsOn
		}
	}
	for start := range deps {
		visited := map[string]bool{start: true}
		cur := deps[start]
		for cur != "" {
			if visited[cur] {
				return nil, fmt.Errorf("%w: circular dependency detected involving role %q", ErrScenarioInvalid, cur)
			}
			visited[cur] = true
			cur = deps[cur]
		}
	}

	sf.dir = filepath.Dir(filepath.Clean(path))
	return &sf, nil
}
