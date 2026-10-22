package server

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"codearts2api/internal/audit"
	"codearts2api/internal/pool"
	"codearts2api/internal/upstream"
)

// classifyChatCause maps the final outcome plus the last upstream error to a
// fixed audit cause. It never stores the provider message: only a class, a
// reduced provider code token and a fixed safe description.
func classifyChatCause(outcome string, err error) audit.Cause {
	switch outcome {
	case "ok":
		return audit.Cause{Class: audit.CauseNone}
	case "timeout":
		return audit.Cause{Class: audit.CauseTimeout, Text: "timeout"}
	case "canceled":
		return audit.Cause{Class: audit.CauseCancel, Text: "canceled"}
	case "canceled_or_timeout", "downstream_canceled":
		if err != nil {
			var downstream *upstream.DownstreamWriteError
			if errors.As(err, &downstream) {
				return audit.Cause{Class: audit.CauseDownstream, Text: "downstream"}
			}
			if isChatTimeout(err) {
				return audit.Cause{Class: audit.CauseTimeout, Text: "timeout"}
			}
			if errors.Is(err, context.Canceled) {
				return audit.Cause{Class: audit.CauseCancel, Text: "canceled"}
			}
		}
		return audit.Cause{Class: audit.CauseCancel, Text: "canceled"}
	case "admission_rejected":
		return audit.Cause{Class: audit.CauseSaturation, Text: "capacity"}
	case "queue_wait_timeout":
		return audit.Cause{Class: audit.CauseQueueWait, Text: "capacity"}
	case "request_body_timeout":
		return audit.Cause{Class: audit.CauseTimeout, Text: "timeout"}
	case "invalid_request":
		if isParamInvalidError(err) {
			return classifyUpstreamError(err)
		}
		return audit.Cause{Class: audit.CauseParamInvalid, Text: "invalid request"}
	case "model_unavailable":
		cause := audit.Cause{Class: audit.CauseCapability, Code: providerCode(errorText(err)), Text: "model not registered"}
		switch capabilityKind(err) {
		case "benefit":
			cause.Text = "benefit not found"
		case "":
			// Keep the class but do not invent a specific capability reason.
			cause.Text = "unknown"
		}
		return cause
	case "upstream_saturated":
		if cause := classifyUpstreamError(err); cause.Class != audit.CauseNone {
			return cause
		}
		return audit.Cause{Class: audit.CauseSaturation, Text: "rate limited"}
	case "upstream_stream_error", "upstream_body_error":
		cause := classifyUpstreamError(err)
		if cause.Class == audit.CauseNone {
			cause.Class = audit.CauseUpstream5xx
			cause.Text = "server error"
		}
		return cause
	case "no_healthy_account", "unavailable":
		if cause := classifyUpstreamError(err); cause.Class != audit.CauseNone {
			return cause
		}
		return audit.Cause{Class: audit.CauseUnknown, Text: "no account"}
	case "upstream_error", "timeout_or_saturation":
		if cause := classifyUpstreamError(err); cause.Class != audit.CauseNone {
			return cause
		}
		return audit.Cause{Class: audit.CauseUnknown, Text: "unknown"}
	}
	return audit.Cause{Class: audit.CauseUnknown, Text: "unknown"}
}

// classifyUpstreamError inspects a concrete provider error.
func classifyUpstreamError(err error) audit.Cause {
	if err == nil {
		return audit.Cause{}
	}
	if capabilityKind(err) != "" {
		text := "model not registered"
		if capabilityKind(err) == "benefit" {
			text = "benefit not found"
		}
		return audit.Cause{Class: audit.CauseCapability, Code: providerCode(err.Error()), Text: text}
	}
	var downstream *upstream.DownstreamWriteError
	if errors.As(err, &downstream) {
		return audit.Cause{Class: audit.CauseDownstream, Text: "downstream"}
	}
	if isChatTimeout(err) {
		return audit.Cause{Class: audit.CauseTimeout, Text: "timeout"}
	}
	if errors.Is(err, context.Canceled) {
		return audit.Cause{Class: audit.CauseCancel, Text: "canceled"}
	}
	if errors.Is(err, pool.ErrQueueFull) || errors.Is(err, pool.ErrAcquireTimeout) {
		return audit.Cause{Class: audit.CauseQueueWait, Text: "capacity"}
	}
	if isParamInvalidError(err) {
		return audit.Cause{Class: audit.CauseParamInvalid, Text: "param invalid", Code: providerCode(err.Error())}
	}
	if isConcurrentUpstreamError(err) {
		return audit.Cause{Class: audit.CauseConcurrencyLimit, Text: "concurrency limit", Code: providerCode(err.Error())}
	}
	var ae *upstream.ApiError
	if errors.As(err, &ae) {
		switch {
		case ae.Status == http.StatusUnauthorized || ae.Code == http.StatusUnauthorized:
			return audit.Cause{Class: audit.CauseAuth, Text: "authentication", Code: providerCode(ae.Message)}
		case ae.Status == http.StatusTooManyRequests:
			return audit.Cause{Class: audit.CauseRateLimit, Text: "rate limited", Code: providerCode(ae.Message)}
		case isQueueLimitError(ae):
			return audit.Cause{Class: audit.CauseSaturation, Text: "rate limited", Code: providerCode(ae.Message)}
		case ae.Status >= 500:
			return audit.Cause{Class: audit.CauseUpstream5xx, Text: "server error", Code: providerCode(ae.Message)}
		}
		return audit.Cause{Class: audit.CauseUnknown, Text: "unknown", Code: providerCode(ae.Message)}
	}
	return audit.Cause{Class: audit.CauseUnknown, Text: "unknown"}
}

// capabilityKind distinguishes the two capability failures we know about.
func capabilityKind(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "002002009") || strings.Contains(msg, "not registered"):
		return "registered"
	case strings.Contains(msg, "4004.200") || strings.Contains(msg, "benefit not found"):
		return "benefit"
	}
	return ""
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// providerCode extracts a CodeArts-style provider code token such as
// InferHub.002002009.404. It returns "" when none is present. The token is a
// code, never a message.
var providerCodePattern = regexp.MustCompile(`(?i)\b(?:InferHub\.(?:ModelArts\.)?[0-9]+(?:\.[0-9]+)*|TM\.[0-9]+(?:\.[0-9]+)*)\b`)

func providerCode(msg string) string {
	code := providerCodePattern.FindString(msg)
	if len(code) > audit.MaxCodeLen {
		code = code[:audit.MaxCodeLen]
	}
	return code
}
