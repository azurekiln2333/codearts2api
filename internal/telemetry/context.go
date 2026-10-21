// Package telemetry carries request-local, metadata-only lifecycle observations.
// Observers must be bounded, perform no I/O and be called outside caller locks.
package telemetry

import "context"

// Phase names describe the work the caller is actually performing. In
// particular, waiting for HTTP response headers is not a TCP-connect timer.
type Phase string

const (
	Admission       Phase = "admission"
	ReadBody        Phase = "read_body"
	Prepare         Phase = "prepare"
	WaitAccount     Phase = "wait_account"
	ValidateAccount Phase = "validate_account"
	WaitRefresh     Phase = "wait_refresh"
	PrepareUpstream Phase = "prepare_upstream"
	WaitHeaders     Phase = "wait_headers"
	ReadError       Phase = "read_error"
	WaitFirstSSE    Phase = "wait_first_sse"
	RetryDelay      Phase = "retry_delay"
	Stream          Phase = "stream"
	Aggregate       Phase = "aggregate"
	WriteResponse   Phase = "write_response"
	Cleanup         Phase = "cleanup"
	Finished        Phase = "finished"
)

// Valid rejects arbitrary strings before they can become log metadata.
func (p Phase) Valid() bool {
	switch p {
	case Admission, ReadBody, Prepare, WaitAccount, ValidateAccount, WaitRefresh,
		PrepareUpstream, WaitHeaders, ReadError, WaitFirstSSE, RetryDelay, Stream,
		Aggregate, WriteResponse, Cleanup, Finished:
		return true
	default:
		return false
	}
}

// Observer sees no payloads, headers, provider errors or credentials. A shared
// background operation must not inherit a caller's observer.
type Observer interface {
	Phase(Phase)
	UpstreamProgress()
	FirstSSEData()
}

type observerKey struct{}

func WithObserver(ctx context.Context, observer Observer) context.Context {
	if observer == nil {
		return ctx
	}
	return context.WithValue(ctx, observerKey{}, observer)
}

func observer(ctx context.Context) Observer {
	if ctx == nil {
		return nil
	}
	o, _ := ctx.Value(observerKey{}).(Observer)
	return o
}

func SetPhase(ctx context.Context, phase Phase) {
	if o := observer(ctx); o != nil && phase.Valid() {
		o.Phase(phase)
	}
}

// UpstreamProgress is called only after a real upstream read returned n > 0.
// It updates a timestamp rather than adding a per-chunk timeline event.
func UpstreamProgress(ctx context.Context) {
	if o := observer(ctx); o != nil {
		o.UpstreamProgress()
	}
}

// FirstSSEData marks accepted SSE data, not a token and not an empty preflight.
func FirstSSEData(ctx context.Context) {
	if o := observer(ctx); o != nil {
		o.FirstSSEData()
	}
}
