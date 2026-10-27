package upstream

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strings"
)

// EpCreditStatistics returns account package metrics; it is not the activity
// delivery endpoint, which only describes claimable campaigns.
const EpCreditStatistics = "/snap-manager/v1/statistics/plugin"

// CreditQuota is a point-in-time upstream credit balance, in CodeArts credits.
// Nil values are unknown, not zero. Categories are deliberately not added to
// Total because the provider's total metric already includes them.
type CreditQuota struct {
	Available bool     `json:"available"`
	Total     *float64 `json:"total"`
	Used      *float64 `json:"used"`
	Remaining *float64 `json:"remaining"`
	ResetDate string   `json:"reset_date,omitempty"`
}

// FetchCreditQuota performs a signed, bounded, read-only statistics request.
func (c *Client) FetchCreditQuota(ctx context.Context, cred SignCredential) (CreditQuota, error) {
	headers := make(http.Header)
	if cred.SecurityToken != "" {
		headers.Set("X-Auth-Token", cred.SecurityToken)
	}
	raw, err := c.modelRequestWithStatusAndHeaders(ctx, http.MethodGet, c.snapURL(EpCreditStatistics), "PromptCenter", cred, 4<<20, http.StatusOK, headers)
	if err != nil {
		return CreditQuota{}, err
	}
	return parseCreditQuota(raw)
}

func parseCreditQuota(raw []byte) (CreditQuota, error) {
	var payload struct {
		Metrics *[]struct {
			Name   string          `json:"name"`
			Amount json.RawMessage `json:"package_credit_amount"`
			Used   json.RawMessage `json:"package_credit_used"`
			Remain json.RawMessage `json:"package_credit_remain"`
		} `json:"metrics"`
		EndDate string `json:"end_date"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || payload.Metrics == nil {
		return CreditQuota{}, errors.New("invalid credit statistics")
	}
	out := CreditQuota{ResetDate: strings.TrimSpace(payload.EndDate)}
	if len(out.ResetDate) > 40 {
		out.ResetDate = ""
	}
	for _, metric := range *payload.Metrics {
		if metric.Name != "usageTotalPackageCredit" {
			continue
		}
		if out.Available {
			return CreditQuota{}, errors.New("duplicate credit total")
		}
		out.Available = true
		var err error
		if out.Total, err = quotaNumber(metric.Amount); err != nil {
			return CreditQuota{}, err
		}
		if out.Used, err = quotaNumber(metric.Used); err != nil {
			return CreditQuota{}, err
		}
		if out.Remaining, err = quotaNumber(metric.Remain); err != nil {
			return CreditQuota{}, err
		}
		if out.Remaining == nil && out.Total != nil && out.Used != nil {
			remaining := math.Max(0, *out.Total-*out.Used)
			out.Remaining = &remaining
		}
		if out.Remaining == nil {
			return CreditQuota{}, errors.New("credit remaining unavailable")
		}
	}
	return out, nil
}

func quotaNumber(raw json.RawMessage) (*float64, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return nil, errors.New("invalid credit statistic")
	}
	return &value, nil
}
