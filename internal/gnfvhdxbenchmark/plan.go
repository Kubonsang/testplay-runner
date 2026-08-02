package gnfvhdxbenchmark

import "fmt"

type Mode string
type Backend string
type Phase string

const (
	ModeSmoke Mode = "smoke"
	ModeFull  Mode = "full"

	BackendLegacy   Backend = "legacy"
	BackendPhysical Backend = "physical-image"
	BackendVHDX     Backend = "vhdx-differencing"

	PhaseGate Phase = "correctness-gate"
	PhaseCold Phase = "cold"
	PhaseWarm Phase = "warm"

	SelectionPlatform = "play_mode"
	SelectionFilter   = "CodexMovementSmokeTest.TestPlayer_MovesRight_InPlayMode"
	SelectionAssembly = "GNF.Tests.PlayMode.dll"
)

var baseOrder = []Backend{BackendLegacy, BackendPhysical, BackendVHDX}

type Selection struct {
	Platform string `json:"platform"`
	Filter   string `json:"filter"`
}

type RunSpec struct {
	ID      string  `json:"id"`
	Phase   Phase   `json:"phase"`
	Round   int     `json:"round"`
	Order   int     `json:"order"`
	Backend Backend `json:"backend"`
}

type Plan struct {
	Mode        Mode      `json:"mode"`
	Concurrency int       `json:"concurrency"`
	Selection   Selection `json:"selection"`
	Runs        []RunSpec `json:"runs"`
}

func BuildPlan(mode Mode) (Plan, error) {
	if mode != ModeSmoke && mode != ModeFull {
		return Plan{}, benchmarkError(CodeInvalidInput, "build-plan", string(mode), fmt.Errorf("mode must be smoke or full"))
	}
	plan := Plan{Mode: mode, Concurrency: 1, Selection: Selection{Platform: SelectionPlatform, Filter: SelectionFilter}}
	appendRound := func(phase Phase, round int, order []Backend) {
		for index, backend := range order {
			plan.Runs = append(plan.Runs, RunSpec{ID: fmt.Sprintf("%s-%02d-%s", phase, round, backend), Phase: phase, Round: round, Order: index + 1, Backend: backend})
		}
	}
	appendRound(PhaseGate, 1, baseOrder)
	if mode == ModeFull {
		appendRound(PhaseCold, 1, baseOrder)
		for round := 1; round <= 10; round++ {
			shift := (round - 1) % len(baseOrder)
			order := append(append([]Backend{}, baseOrder[shift:]...), baseOrder[:shift]...)
			appendRound(PhaseWarm, round, order)
		}
	}
	return plan, nil
}
