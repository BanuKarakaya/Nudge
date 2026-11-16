package analytics

import "context"

type OperationalError struct {
	Code  string
	Cause error
}

func (e *OperationalError) Error() string  { return e.Cause.Error() }
func (e *OperationalError) Unwrap() error  { return e.Cause }
func Failure(code string, err error) error { return &OperationalError{Code: code, Cause: err} }

// LiveEvent contains operational metadata only. Never put prompts, tool arguments,
// provider error bodies, credentials or generated responses in this structure.
type LiveEvent struct {
	Stage      string   `json:"stage"`
	Model      string   `json:"model,omitempty"`
	Tool       string   `json:"tool,omitempty"`
	ErrorCode  string   `json:"error_code,omitempty"`
	DurationMS int64    `json:"duration_ms,omitempty"`
	Usage      *AIUsage `json:"-"`
}
type reporterKey struct{}

func WithReporter(ctx context.Context, report func(LiveEvent)) context.Context {
	return context.WithValue(ctx, reporterKey{}, report)
}
func Report(ctx context.Context, e LiveEvent) {
	if report, ok := ctx.Value(reporterKey{}).(func(LiveEvent)); ok {
		report(e)
	}
}
func Stage(ctx context.Context, stage string) { Report(ctx, LiveEvent{Stage: stage}) }

// Validate uses a host-owned catalog. It cannot become an arbitrary log sink.
func ValidStage(stage string) bool {
	switch stage {
	case "worker_started", "identity_lookup", "installation_lookup", "history_loading", "agent_starting", "model_starting", "model_running", "model_handoff", "tool_started", "tool_completed", "tool_failed", "approval_waiting", "action_starting", "action_completed", "action_failed", "response_ready", "delivery_started", "delivery_failed", "delivery_completed", "attempt_failed", "usage":
		return true
	}
	return false
}
