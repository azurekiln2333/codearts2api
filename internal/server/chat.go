package server

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"codearts2api/internal/audit"
	"codearts2api/internal/pool"
	"codearts2api/internal/upstream"
)

// auditOutcome refines the internal outcome into a truthful record value.
// The lifecycle helpers collapse "client canceled" and "timed out" into one
// string; a record must not, because the operator action differs.
func auditOutcome(outcome string, cause audit.Cause, canceled bool) string {
	switch outcome {
	case "canceled_or_timeout":
		if cause.Class == audit.CauseTimeout {
			return "timeout"
		}
		return "canceled"
	case "timeout_or_saturation":
		switch cause.Class {
		case audit.CauseTimeout:
			return "timeout"
		case audit.CauseQueueWait:
			return "queue_wait_timeout"
		}
		if canceled {
			return "canceled"
		}
		return "unavailable"
	case "downstream_canceled":
		return "downstream_canceled"
	case "unavailable":
		if cause.Class == audit.CauseQueueWait {
			return "queue_wait_timeout"
		}
		return outcome
	}
	return outcome
}

func (h *Handler) chatCompletions(original http.ResponseWriter, r *http.Request) {
	started := time.Now()
	// A stable correlation id is generated for every request, independent of
	// whether audit is enabled, so logs and records can always be joined.
	requestID := randHex(16)
	original.Header().Set("X-Request-Id", requestID)
	lifetime := newChatLifetime(r.Context(), h.cfg.StartupTimeout)
	defer lifetime.close()
	w := newChatResponseWriter(original, h.cfg.StreamWriteTimeout)
	defer w.clearDeadline()
	model := h.cfg.DefaultModel
	outcome, accountName, attempts := "invalid_request", "", 0
	var waited, firstData time.Duration
	var (
		streamFlag     bool
		requestedModel string
		canonical      string
		triedOrder     []string
		auditErr       error
	)
	// Registered first, so it runs last: after the attempt lease/body cleanup
	// below. The sink call is a non-blocking enqueue and never holds a lock or
	// a slot when it runs.
	defer func() {
		total := time.Since(started)
		log.Printf("chat id=%s model=%s account=%s attempts=%d wait=%s first_data=%s total=%s outcome=%s", requestID, model, accountName, attempts,
			waited.Round(time.Millisecond), firstData.Round(time.Millisecond), total.Round(time.Millisecond), outcome)
		if h.cfg.Audit == nil {
			return
		}
		status, committed := w.Status()
		canceled := r.Context().Err() != nil
		// The final context cause takes precedence over an earlier provider
		// failure. Acquire/refresh return context.Canceled for both caller
		// cancellation and our startup deadline; they are different outcomes.
		recordOutcome, recordErr := outcome, auditErr
		if outcome != "ok" && outcome != "request_body_timeout" {
			switch {
			case canceled:
				recordOutcome, recordErr = "canceled", r.Context().Err()
			case w.err != nil:
				recordOutcome, recordErr = "downstream_canceled", &upstream.DownstreamWriteError{Err: w.err}
			case isChatTimeout(context.Cause(lifetime.ctx)):
				recordOutcome, recordErr = "timeout", context.Cause(lifetime.ctx)
			}
		}
		cause := classifyChatCause(recordOutcome, recordErr)
		// TS is the request start time, not the completion time.
		ev := audit.Event{
			ID:             requestID,
			TS:             started.UTC().Format(time.RFC3339Nano),
			Model:          requestedModel,
			ModelCanonical: canonical,
			Stream:         streamFlag,
			Committed:      committed,
			Status:         status,
			Outcome:        auditOutcome(recordOutcome, cause, canceled),
			ErrorCode:      w.ErrorCode(),
			Attempts:       attempts,
			Tried:          triedOrder,
			WaitMS:         waited.Milliseconds(),
			QueueMS:        waited.Milliseconds(),
			TotalMS:        total.Milliseconds(),
			Canceled:       canceled,
			Cause:          cause,
		}
		// Capture the Retry-After actually offered to the client.
		if v := w.Header().Get("Retry-After"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				ev.RetryAfterS = n
			}
		}
		if accountName != "" {
			name := accountName
			ev.Account = &name
		}
		if firstData > 0 {
			ms := firstData.Milliseconds()
			ev.FirstDataMS = &ms
		}
		h.cfg.Audit.Record(ev)
	}()

	// Include request reads and retry sleeps in the process-wide limit, across
	// handlers sharing this pool. Fast 429s must not accumulate sleeper waves.
	releaseAdmission, err := h.cfg.Pool.AdmitRequest(lifetime.ctx)
	if err != nil {
		outcome = "admission_rejected"
		auditErr = err
		writeChatLifecycleError(w, r, lifetime, err)
		return
	}
	defer releaseAdmission()
	controller := http.NewResponseController(original)
	readDeadlineSupported := controller.SetReadDeadline(started.Add(h.cfg.StartupTimeout)) == nil
	stopRead := context.AfterFunc(lifetime.ctx, func() {
		if readDeadlineSupported {
			_ = controller.SetReadDeadline(time.Now())
		} else {
			_ = r.Body.Close()
		}
	})
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
	stopRead()
	if err == nil && lifetime.ctx.Err() == nil {
		_ = controller.SetReadDeadline(time.Time{})
	} else {
		// A partial request body must not keep a native connection alive or let
		// net/http drain it indefinitely after the handler has timed out.
		r.Close = true
		w.Header().Set("Connection", "close")
	}
	if isChatTimeout(err) {
		// net/http cancels Request.Context on a body read deadline as well as
		// on peer disconnect. This deadline belongs to our startup budget.
		outcome = "request_body_timeout"
		auditErr = err
		writeOpenAIError(w, http.StatusGatewayTimeout, "request_timeout", "request body timed out")
		return
	}
	if writeChatLifecycleError(w, r, lifetime, err) {
		outcome = "canceled_or_timeout"
		return
	}
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "read body: "+err.Error())
		return
	}
	if len(body) > maxBodyBytes {
		writeOpenAIError(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body exceeds 8MB limit")
		return
	}
	req, err := parseChatRequest(body)
	if err != nil {
		auditErr = err
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	// Requested model is recorded exactly as sent (literal "auto" included) and
	// is available even when a later validation step rejects the request.
	requestedModel = req.Model
	streamFlag = req.Stream
	if req.Model != "" && req.Model != "auto" {
		model = req.Model
	}
	canonical = upstream.CanonicalModel(model)
	tools, err := nativeTools(req.Tools, req.ToolChoice)
	if err != nil {
		auditErr = err
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	affinity := r.Header.Get("X-Codearts-Chat-Id")
	if affinity == "" {
		affinity = r.Header.Get("X-Session-Affinity")
	}
	if affinity == "" {
		affinity = r.Header.Get("X-Session-Id")
	}
	stickyAllowed := req.ConversationID != "" || affinity != ""
	for _, message := range req.Messages {
		if message.Role == "assistant" || message.Role == "tool" || message.Role == "function" {
			stickyAllowed = true
			break
		}
	}
	req.ConversationID = conversationIDFor(req, affinity)
	toolsOn := toolsActive(req)
	if req.Model != "" && req.Model != "auto" {
		model = req.Model
		canonical = upstream.CanonicalModel(req.Model)
	}
	outcome = "unavailable"

	explicitChat := validChatID(req.ConversationID)
	stickyAcct := ""
	if explicitChat && stickyAllowed {
		h.convMu.Lock()
		stickyAcct = h.convAcct[req.ConversationID]
		h.convMu.Unlock()
	}
	msgs := buildUpstreamMessages(req, toolsOn)
	chatID := req.ConversationID
	if !validChatID(chatID) {
		chatID = randHex(32)
	}
	chatOpts := upstream.ChatOptions{
		ReasoningEffort: req.ReasoningEffort,
		MaxTokens:       req.MaxTokens, Temperature: req.Temperature, TopP: req.TopP,
		Tools: tools, ToolChoice: nativeToolChoice(req.ToolChoice),
	}

	tried := map[string]bool{}
	var lastErr error
	var current *chatAttempt
	defer func() { current.close() }()
	remainingWait := h.cfg.AccountWaitTimeout
	acquire := func(candidates []pool.AccountCandidate) (*pool.Lease, error) {
		before := time.Now()
		lease, err := h.cfg.Pool.AcquireAccount(lifetime.ctx, candidates, remainingWait)
		elapsed := time.Since(before)
		waited += elapsed
		remainingWait -= elapsed
		if remainingWait < 0 {
			remainingWait = 0
		}
		return lease, err
	}

	// Rotation budget: only generic provider failures (5xx, 429, transport,
	// timeouts) are charged. A capability rejection (model not registered /
	// benefit not found) says nothing about account health or the next
	// account, so it must not consume this budget — otherwise three
	// capability-refusing accounts can exhaust MaxRotate and report a model
	// unavailable while later eligible accounts were never tried. The tried
	// set (finite) and the shared startup deadline still bound the loop.
	rotations := 0
	maxIterations := h.cfg.MaxRotate + len(h.cfg.Pool.Accounts()) + 1
	for iteration := 0; iteration < maxIterations && rotations < h.cfg.MaxRotate; iteration++ {
		if writeChatLifecycleError(w, r, lifetime, nil) {
			outcome = "canceled_or_timeout"
			return
		}
		candidates := h.chatCandidates(tried, model, stickyAcct)
		if len(candidates) == 0 {
			break
		}
		lease, aerr := acquire(candidates)
		if aerr != nil {
			auditErr = aerr
			if writeChatLifecycleError(w, r, lifetime, aerr) {
				// Distinguish "waited too long for a slot" from "the ticket cap
				// rejected this request": the operator action differs.
				if errors.Is(aerr, pool.ErrAcquireTimeout) {
					outcome = "queue_wait_timeout"
				} else {
					outcome = "admission_rejected"
				}
				return
			}
			lastErr = aerr
			// A sticky account can become disabled while waiting. Re-evaluate
			// cached candidates without charging an upstream attempt for it.
			if stickyAcct != "" && errors.Is(aerr, pool.ErrNoHealthyAccount) {
				stickyAcct = ""
				iteration-- // Not charged: no upstream attempt was made.
				continue
			}
			break
		}
		acct := lease.Account
		current = &chatAttempt{lease: lease}
		tried[acct.Name] = true
		if len(triedOrder) < audit.MaxTried {
			triedOrder = append(triedOrder, acct.Name)
		}
		accountName = acct.Name
		stickyAcct = "" // Only the first still-valid affinity choice is pinned.

		valid, verr := h.cfg.Pool.ValidateContext(lifetime.ctx, acct)
		if verr != nil || !valid {
			current.close()
			auditErr = verr
			if writeChatLifecycleError(w, r, lifetime, verr) {
				outcome = "canceled_or_timeout"
				return
			}
			lastErr = verr
			if lastErr == nil {
				lastErr = errors.New("account token is invalid")
				auditErr = lastErr
			}
			rotations++ // Account-level failure, not model capability.
			continue
		}

		authRetried := false
		var sendErr error
		for attempt := 0; attempt < h.cfg.QueueMaxAttempts; attempt++ {
			if writeChatLifecycleError(w, r, lifetime, nil) {
				outcome = "canceled_or_timeout"
				return
			}
			authz := acct.AuthSnapshot()
			if authz == nil {
				sendErr = errors.New("account credentials are missing")
				auditErr = sendErr
				break
			}
			token, ak, sk := authz.Credentials()
			cred := upstream.SignCredential{AccessKeyID: ak, SecretAccessKey: sk, SecurityToken: token}
			noteTraffic(acct.UID)
			h.cfg.Pool.PingKeepalive(acct.Name)
			attempts++
			current.body, sendErr = acct.Client.ChatStreamWithOptions(lifetime.ctx, chatID, msgs, "", cred,
				authz.UserName, model, chatOpts, upstream.IsBenefitModel(acct.UID, model))
			if sendErr == nil {
				break
			}
			if writeChatLifecycleError(w, r, lifetime, sendErr) {
				outcome = "canceled_or_timeout"
				auditErr = sendErr
				return
			}
			if current.body != nil {
				_ = current.body.Close()
				current.body = nil
			}
			var ae *upstream.ApiError
			if errors.As(sendErr, &ae) && (ae.Status == 401 || ae.Code == 401) && !authRetried && authz.Refresh() != "" {
				authRetried = true
				refreshErr := h.cfg.Pool.RefreshTokenContext(lifetime.ctx, acct.Name)
				if writeChatLifecycleError(w, r, lifetime, refreshErr) {
					outcome = "canceled_or_timeout"
					auditErr = refreshErr
					return
				}
				if refreshErr == nil {
					attempt-- // The single refresh retry is independent of queue attempts.
					continue
				}
			}
			if !errors.As(sendErr, &ae) || !isQueueLimitError(ae) || attempt+1 >= h.cfg.QueueMaxAttempts {
				break
			}
			current.close()
			current = nil
			if err := waitChatRetry(lifetime.ctx, h.cfg.QueueRetryDelay); err != nil {
				outcome = "canceled_or_timeout"
				auditErr = err
				writeChatLifecycleError(w, r, lifetime, err)
				return
			}
			lease, err = acquire([]pool.AccountCandidate{{Name: acct.Name}})
			if err != nil {
				auditErr = err
				if writeChatLifecycleError(w, r, lifetime, err) {
					// Retry re-acquisition timing out is a queue wait, not a ticket
					// cap rejection.
					if errors.Is(err, pool.ErrAcquireTimeout) {
						outcome = "queue_wait_timeout"
					} else {
						outcome = "admission_rejected"
					}
					return
				}
				lastErr = err
				break
			}
			current = &chatAttempt{lease: lease}
		}
		if current == nil {
			// Failed reacquisition owns no slot and is not a provider failure.
			continue
		}
		if sendErr != nil {
			current.close()
			lastErr = sendErr
			auditErr = sendErr
			if isParamInvalidError(sendErr) {
				outcome = "invalid_request"
				writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "upstream rejected request params: "+truncateMsg(sendErr.Error(), 200))
				return
			}
			// Account-specific capability failures may succeed on another account
			// and are not charged against the generic rotation budget.
			capabilityOnly := h.handleUpstreamError(acct, model, sendErr)
			if !capabilityOnly {
				rotations++
			}
			continue
		}
		if err := lifetime.started(); err != nil {
			outcome = "canceled_or_timeout"
			writeChatLifecycleError(w, r, lifetime, err)
			return
		}
		firstData = time.Since(started)
		w.Header().Set("X-Codearts-Chat-Id", chatID)
		storeChat := func() {
			if !explicitChat {
				return
			}
			h.convMu.Lock()
			h.chats[acct.Name] = chatID
			h.convAcct[req.ConversationID] = acct.Name
			h.convMu.Unlock()
			h.saveChats()
		}
		if req.Stream {
			var usage func(*upstream.RawCompletion) map[string]any
			if req.IncludeUsage {
				usage = func(comp *upstream.RawCompletion) map[string]any { return usageEstimate(msgs, comp) }
			}
			streamErr := upstream.StreamCaptureWithUsage(w, current.body, model, nil, usage)
			current.close()
			if w.err != nil || r.Context().Err() != nil {
				outcome = "downstream_canceled"
				return
			}
			if streamErr != nil {
				outcome = "upstream_stream_error"
				auditErr = streamErr
				h.handleUpstreamError(acct, model, streamErr)
				return
			}
			storeChat()
			markUsable(acct.UID, model)
			h.cfg.Pool.PingKeepalive(acct.Name)
			h.cfg.Pool.NoteSuccess(acct.Name)
			outcome = "ok"
			return
		}

		comp, aggregateErr := upstream.AggregateRaw(current.body)
		current.close()
		if aggregateErr != nil {
			auditErr = aggregateErr
			if writeChatLifecycleError(w, r, lifetime, aggregateErr) {
				outcome = "canceled_or_timeout"
				return
			}
			if isParamInvalidError(aggregateErr) {
				outcome = "invalid_request"
				writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "upstream rejected request params: "+truncateMsg(aggregateErr.Error(), 200))
				return
			}
			// A first-data success ends the startup/failover phase. A late error
			// must be surfaced rather than silently regenerating a partial reply.
			h.handleUpstreamError(acct, model, aggregateErr)
			outcome = "upstream_body_error"
			status := http.StatusBadGateway
			code := "upstream_error"
			if upstreamUnavailableReason(aggregateErr) != "" {
				status, code = http.StatusNotFound, "model_not_available"
			}
			writeOpenAIError(w, status, code, truncateMsg(aggregateErr.Error(), 200))
			return
		}
		if writeChatLifecycleError(w, r, lifetime, nil) {
			outcome = "downstream_canceled"
			return
		}
		content, finish := comp.Content, comp.Finish
		calls := fromUpstreamToolCalls(comp.ToolCalls)
		if toolsOn && len(calls) == 0 {
			if c, cleanContent, found := toolResponse(comp.Content + "\n" + comp.Reasoning); found {
				calls, content, finish = c, cleanContent, "tool_calls"
				assignCallIDs(calls)
			}
		}
		if len(calls) > 0 {
			finish = "tool_calls"
		}
		writeJSON(w, http.StatusOK, buildCompletion(model, comp.Reasoning, content, calls, finish, chatID, usageEstimate(msgs, comp)))
		if w.err != nil {
			outcome = "downstream_canceled"
			return
		}
		storeChat()
		markUsable(acct.UID, model)
		h.cfg.Pool.NoteSuccess(acct.Name)
		outcome = "ok"
		return
	}
	if writeChatLifecycleError(w, r, lifetime, lastErr) {
		outcome = "timeout_or_saturation"
		auditErr = lastErr
		return
	}
	auditErr = lastErr
	// Truthful unavailability: only claim the model is unavailable when every
	// eligible candidate was actually evaluated. Remaining untried candidates
	// means the rotation budget stopped us, which is not the same thing.
	if len(h.chatCandidates(tried, model, "")) == 0 && (h.modelKnownUnusable(model) || upstreamUnavailableReason(lastErr) != "") {
		outcome = "model_unavailable"
		writeOpenAIError(w, http.StatusNotFound, "model_not_available", "model "+model+" is not available on eligible configured accounts")
		return
	}
	var ae *upstream.ApiError
	if errors.As(lastErr, &ae) && isQueueLimitError(ae) {
		outcome = "upstream_saturated"
		w.Header().Set("Retry-After", "2")
		writeOpenAIError(w, http.StatusTooManyRequests, "upstream_busy", "upstream is busy; retry later")
		return
	}
	outcome = "no_healthy_account"
	writeOpenAIError(w, http.StatusServiceUnavailable, "no_healthy_account", "no eligible account is currently available")
}

// Call before account health updates or failover. A timeout can originate in the
// upstream idle guard while the caller remains live, so inspect context causes
// separately from the error's context.DeadlineExceeded compatibility.
func writeChatLifecycleError(w http.ResponseWriter, r *http.Request, l *chatLifetime, err error) bool {
	if r.Context().Err() != nil {
		return true
	}
	cause := context.Cause(l.ctx)
	if isChatTimeout(cause) || isChatTimeout(err) {
		writeOpenAIError(w, http.StatusGatewayTimeout, "upstream_timeout", "chat startup or upstream inactivity timed out")
		return true
	}
	if cause != nil || errors.Is(err, context.Canceled) {
		return true
	}
	var downstream *upstream.DownstreamWriteError
	if errors.As(err, &downstream) {
		return true
	}
	if errors.Is(err, pool.ErrQueueFull) || errors.Is(err, pool.ErrAcquireTimeout) {
		w.Header().Set("Retry-After", "1")
		writeOpenAIError(w, http.StatusTooManyRequests, "server_busy", "account capacity is busy; retry later")
		return true
	}
	return false
}
