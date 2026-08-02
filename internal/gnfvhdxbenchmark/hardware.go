package gnfvhdxbenchmark

import "context"

type HardwareConfig struct {
	EditorPath     string
	ProjectPath    string
	WorkRoot       string
	ArtifactRoot   string
	HelperPath     string
	Mode           Mode
	ParentBytes    int64
	SourceRevision string
}

func RunHardware(ctx context.Context, config HardwareConfig) (Summary, error) {
	return runHardware(ctx, config)
}
