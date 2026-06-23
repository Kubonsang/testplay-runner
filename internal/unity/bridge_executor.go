package unity

import (
	"context"
	"errors"

	"github.com/Kubonsang/testplay-runner/internal/bridge"
	"github.com/Kubonsang/testplay-runner/internal/history"
	"github.com/Kubonsang/testplay-runner/internal/status"
)

// BridgeClient is the warm-editor bridge transport ExecuteBridge drives. It is
// satisfied by *bridge.Client; an interface is used so the executor can be
// tested without real bridge files. Defined consumer-side (Go idiom).
type BridgeClient interface {
	Run(ctx context.Context, req bridge.RunRequest, sw status.WriterInterface) (bridge.RunOutcome, error)
}

// BridgeResult is the outcome of ExecuteBridge. Result/ExitCode mirror what
// Execute returns so runsvc reuses the identical post-execution pipeline. When
// FellBack is true the bridge declined to produce a trustworthy result and the
// caller MUST run the cold path instead (Result/ExitCode are unset).
type BridgeResult struct {
	Result   *history.RunResult
	ExitCode int
	FellBack bool
	Warnings []string // non-pristine disclosures → run warnings
}

// ExecuteBridge runs tests through a warm-editor bridge and classifies the
// outcome into the SAME RunResult + exit code the batchmode executor produces,
// reusing parseResults (for the results.xml the bridge writes), classifyNoResults
// (for the 2-vs-6 split from the bridge's compile-errors sidecar / build-failed
// signal), and handleContextErr (for timeout/interrupt). The bridge runs against
// the real project, so absolute_path fields are already source-correct and need
// no shadow remap.
//
// runID correlates the bridge request/response files and the results.xml path.
// idleDeadlineMs bounds how long the bridge may wait for the editor to settle
// before answering busy.
func ExecuteBridge(ctx context.Context, client BridgeClient, opts ExecuteOptions, runID string, idleDeadlineMs int64) BridgeResult {
	if opts.StatusWriter == nil {
		opts.StatusWriter = noopStatusWriter{}
	}

	// Phase: compiling. The bridge refreshes assets and waits for compilation
	// to settle before running; streamed progress refines this to running.
	_ = opts.StatusWriter.Write(status.Status{Phase: status.PhaseCompiling})

	req := bridge.RunRequest{
		RunID:          runID,
		TestPlatform:   opts.TestPlatform,
		Filter:         opts.Filter,
		Category:       opts.Category,
		ResultsXML:     opts.ResultsFile,
		IdleDeadlineMs: idleDeadlineMs,
	}

	outcome, err := client.Run(ctx, req, opts.StatusWriter)
	if err != nil {
		// Context cancellation/deadline → the SAME exit 4/8 a cold run returns.
		// This is a legitimate result, NOT a fall-back.
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			result, code := handleContextErr(ctx, err, opts)
			return BridgeResult{Result: result, ExitCode: code}
		}
		// Any other IO/transport error means we could not obtain a trustworthy
		// result; the honest move is to fall back to the cold path.
		return BridgeResult{FellBack: true}
	}

	switch outcome.Outcome {
	case bridge.OutcomeCompleted:
		// The bridge must have written results.xml. If it claims completion but
		// did not write the XML (results_xml_written=false), fall back to cold —
		// never derive a result from a missing file.
		if !outcome.ResultsXMLWritten {
			return BridgeResult{FellBack: true}
		}
		// The bridge wrote results.xml to opts.ResultsFile; classify it exactly
		// as batchmode does (parseResults also writes the terminal done phase).
		result, code := parseResults(opts, nil)
		// A "completed" outcome must yield real results (exit 0/3). An exit 2
		// here means the XML is missing/unparseable despite the bridge's claim;
		// fall back to cold rather than report a phantom compile failure.
		if code == 2 {
			return BridgeResult{FellBack: true}
		}
		return BridgeResult{Result: result, ExitCode: code, Warnings: outcome.NonPristine}

	case bridge.OutcomeCompileFailed:
		_ = opts.StatusWriter.Write(status.Status{Phase: status.PhaseDone})
		result, code := classifyNoResults(outcome.CompileErrors, false)
		return BridgeResult{Result: result, ExitCode: code, Warnings: outcome.NonPristine}

	case bridge.OutcomeBuildFailed:
		_ = opts.StatusWriter.Write(status.Status{Phase: status.PhaseDone})
		result, code := classifyNoResults(nil, true)
		return BridgeResult{Result: result, ExitCode: code, Warnings: outcome.NonPristine}

	default:
		// busy, rejected, or any unrecognized outcome → fall back to cold.
		return BridgeResult{FellBack: true}
	}
}
