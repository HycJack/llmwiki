// Package runtime is a minimal replacement for ragflow/internal/agent/runtime,
// providing only the progress-reporting seam that the LLM-wiki knowledge
// compiler uses. In RAGFlow this is wired to a per-run component-message sink;
// here it is a lightweight no-op-agnostic hook so callers can observe pipeline
// progress (MAP/REDUCE/PLAN/REFINE) without the full agent canvas.
package runtime

import "context"

// ProgressMessageCallback receives detailed stage messages emitted by a
// compiler while it runs (e.g. "Wiki MAP Done: ...").
type ProgressMessageCallback func(component, message string)

type progressMessageCallbackKey struct{}

// WithProgressMessageCallback attaches a message sink to a run context. A nil
// callback is valid and keeps the compiler a pure function.
func WithProgressMessageCallback(ctx context.Context, cb ProgressMessageCallback) context.Context {
	return context.WithValue(ctx, progressMessageCallbackKey{}, cb)
}

// ProgressCallbackFromCtx returns the installed sink (nil when none).
func ProgressCallbackFromCtx(ctx context.Context) ProgressMessageCallback {
	if cb, ok := ctx.Value(progressMessageCallbackKey{}).(ProgressMessageCallback); ok {
		return cb
	}
	return nil
}

// ReportProgressMessage forwards a detailed component message when a sink is
// attached. Callers (wiki/structure/jsonchat) rely on this being resolvable and
// cheap; a missing sink is a no-op.
func ReportProgressMessage(ctx context.Context, component, message string) {
	if cb := ProgressCallbackFromCtx(ctx); cb != nil {
		cb(component, message)
	}
}

// ReportComponentFraction forwards a 0..1 fraction for a stage. It is a no-op
// by default; mirror it to ReportProgressMessage when a sink is attached.
func ReportComponentFraction(ctx context.Context, fraction float64) {
	// The wiki variant tracks page-completion as a message, not a fraction;
	// this seam is kept for parity with the structure/tree variants.
}
