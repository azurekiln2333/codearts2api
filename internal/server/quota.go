package server

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"strings"
)

// StartQuotaCollection binds the read-only collector to the server lifetime.
func (h *Handler) StartQuotaCollection(ctx context.Context) {
	if h != nil && h.cfg.Pool != nil {
		h.cfg.Pool.StartQuotaCollection(ctx)
	}
}

// handleQuota is cache-only. A credit-to-USD rate must be supplied explicitly
// before returning the credit_summary format consumed by New API's balance UI.
func (h *Handler) handleQuota(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	snapshot := h.cfg.Pool.QuotaSnapshot()
	values, supplied := r.URL.Query()["credits_per_usd"]
	if !supplied {
		writeJSON(w, http.StatusOK, map[string]any{
			"object": "codearts_credit_summary", "unit": snapshot.Unit,
			"scope": snapshot.Scope, "overlap_possible": snapshot.OverlapPossible,
			"credit_packages_only": snapshot.CreditPackagesOnly, "excludes_benefit_quota": snapshot.ExcludesBenefitQuota,
			"state": snapshot.State, "complete": snapshot.Complete,
			"total": snapshot.Total, "used": snapshot.Used, "remaining": snapshot.Remaining, "known_remaining": snapshot.KnownRemaining,
			"configured_accounts": snapshot.ConfiguredAccounts, "counted_accounts": snapshot.CountedAccounts,
			"ready_accounts": snapshot.ReadyAccounts, "failed_accounts": snapshot.FailedAccounts,
			"unknown_accounts": snapshot.UnknownAccounts, "unavailable_accounts": snapshot.UnavailableAccounts,
			"stale_accounts": snapshot.StaleAccounts, "duplicate_accounts": snapshot.DuplicateAccounts,
			"unprocessed_accounts": snapshot.UnprocessedAccounts, "oldest_success_at": snapshot.OldestSuccessAt,
			"refresh": snapshot.Refresh,
		})
		return
	}
	if len(values) != 1 {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "credits_per_usd must be one positive finite number")
		return
	}
	rate, err := strconv.ParseFloat(strings.TrimSpace(values[0]), 64)
	if err != nil || rate <= 0 || math.IsNaN(rate) || math.IsInf(rate, 0) {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "credits_per_usd must be one positive finite number")
		return
	}
	if !snapshot.Complete || snapshot.Remaining == nil {
		writeOpenAIError(w, http.StatusServiceUnavailable, "quota_unavailable", "credit snapshot is incomplete or stale")
		return
	}
	amount := *snapshot.Remaining / rate
	if math.IsNaN(amount) || math.IsInf(amount, 0) || (amount == 0 && *snapshot.Remaining > 0) {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "credits_per_usd produces an invalid amount")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"object": "credit_summary", "total_available": amount, "unit": "USD",
		"credit_unit": "credits", "credits_per_usd": rate,
		"available_credits": snapshot.Remaining, "scope": snapshot.Scope,
		"overlap_possible": snapshot.OverlapPossible, "credit_packages_only": snapshot.CreditPackagesOnly,
		"excludes_benefit_quota": snapshot.ExcludesBenefitQuota, "oldest_success_at": snapshot.OldestSuccessAt,
	})
}

func (h *Handler) adminQuotaRefresh(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusAccepted, map[string]any{"refresh": h.cfg.Pool.RequestQuotaRefresh()})
}
