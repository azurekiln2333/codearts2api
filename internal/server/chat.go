package server

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"time"

	"codearts2api/internal/pool"
	"codearts2api/internal/upstream"
)

func (h *Handler) chatCompletions(original http.ResponseWriter, r *http.Request) {
	started := time.Now()
	lifetime := newChatLifetime(r.Context(), h.cfg.StartupTimeout)
	defer lifetime.close()
	w := newChatResponseWriter(original, h.cfg.StreamWriteTimeout)
	defer w.clearDeadline()
	model := h.cfg.DefaultModel
	outcome, accountName, attempts := "invalid_request", "", 0
	var waited, firstData time.Duration
	defer func() {
		log.Printf("chat model=%s account=%s attempts=%d wait=%s first_data=%s total=%s outcome=%s", model, accountName, attempts,
			waited.Round(time.Millisecond), firstData.Round(time.Millisecond), time.Since(started).Round(time.Millisecond), outcome)
	}()

	// Include request reads and retry sleeps in the process-wide limit, across
	// handlers sharing this pool. Fast 429s must not accumulate sleeper waves.
	releaseAdmission, err := h.cfg.Pool.AdmitRequest(lifetime.ctx)
	if err != nil {
		outcome = "admission_rejected"
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
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	tools, err := nativeTools(req.Tools, req.ToolChoice)
	if err != nil {
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

	for rotation := 0; rotation < h.cfg.MaxRotate; rotation++ {
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
			if writeChatLifecycleError(w, r, lifetime, aerr) {
				outcome = "admission_rejected"
				return
			}
			lastErr = aerr
			// A sticky account can become disabled while waiting. Re-evaluate
			// cached candidates without charging an upstream attempt for it.
			if stickyAcct != "" && errors.Is(aerr, pool.ErrNoHealthyAccount) {
				stickyAcct = ""
				rotation--
				continue
			}
			break
		}
		acct := lease.Account
		current = &chatAttempt{lease: lease}
		tried[acct.Name] = true
		accountName = acct.Name
		stickyAcct = "" // Only the first still-valid affinity choice is pinned.

		valid, verr := h.cfg.Pool.ValidateContext(lifetime.ctx, acct)
		if verr != nil || !valid {
			current.close()
			if writeChatLifecycleError(w, r, lifetime, verr) {
				outcome = "canceled_or_timeout"
				return
			}
			lastErr = verr
			if lastErr == nil {
				lastErr = errors.New("account token is invalid")
			}
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
				writeChatLifecycleError(w, r, lifetime, err)
				return
			}
			lease, err = acquire([]pool.AccountCandidate{{Name: acct.Name}})
			if err != nil {
				if writeChatLifecycleError(w, r, lifetime, err) {
					outcome = "admission_rejected"
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
			if isParamInvalidError(sendErr) {
				outcome = "invalid_request"
				writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "upstream rejected request params: "+truncateMsg(sendErr.Error(), 200))
				return
			}
			// Account-specific capability failures may succeed on another account.
			h.handleUpstreamError(acct, model, sendErr)
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
		return
	}
	if h.modelKnownUnusable(model) || upstreamUnavailableReason(lastErr) != "" {
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
