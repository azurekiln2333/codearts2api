package audit

import (
	"bytes"
	"encoding/json"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	UsageSourceUpstream  = "upstream"
	UsageSourceEstimated = "estimated"
	// These values remain exactly representable by the panel's JSON numbers.
	MaxTokenCount  int64 = 1<<53 - 1
	MaxUsageGroups       = 100
)

// TokenUsage stores generation metadata only. A nil counter is unknown, whereas
// a non-nil zero is a reported zero. Source never contains provider text.
type TokenUsage struct {
	PromptTokens     *int64 `json:"prompt_tokens"`
	CompletionTokens *int64 `json:"completion_tokens"`
	TotalTokens      *int64 `json:"total_tokens"`
	Source           string `json:"source"`
}

// UsageFromMap selects only reviewed numeric counters from a response's usage.
// It never keeps arbitrary provider fields or fills missing native components
// with estimates. An omitted total can be derived from two known components.
func UsageFromMap(value map[string]any, source string) *TokenUsage {
	if source != UsageSourceUpstream && source != UsageSourceEstimated {
		return nil
	}
	u := &TokenUsage{
		PromptTokens:     tokenCount(value["prompt_tokens"]),
		CompletionTokens: tokenCount(value["completion_tokens"]),
		TotalTokens:      tokenCount(value["total_tokens"]),
		Source:           source,
	}
	if _, provided := value["total_tokens"]; !provided && u.PromptTokens != nil && u.CompletionTokens != nil {
		if *u.CompletionTokens <= MaxTokenCount-*u.PromptTokens {
			total := *u.PromptTokens + *u.CompletionTokens
			u.TotalTokens = &total
		}
	}
	return sanitizeTokenUsage(u)
}

// UnmarshalJSON isolates malformed optional usage from an otherwise valid audit
// event. Replay retains the record with unknown usage instead of fabricating a
// count or discarding its independent status, identity and timing metadata.
func (u *TokenUsage) UnmarshalJSON(data []byte) error {
	*u = TokenUsage{}
	var fields map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if decoder.Decode(&fields) != nil {
		return nil
	}
	source, _ := fields["source"].(string)
	if usage := UsageFromMap(fields, source); usage != nil {
		*u = *usage
	}
	return nil
}

func tokenCount(value any) *int64 {
	var count int64
	switch v := value.(type) {
	case int:
		count = int64(v)
	case int8:
		count = int64(v)
	case int16:
		count = int64(v)
	case int32:
		count = int64(v)
	case int64:
		count = v
	case uint:
		if uint64(v) > uint64(MaxTokenCount) {
			return nil
		}
		count = int64(v)
	case uint8:
		count = int64(v)
	case uint16:
		count = int64(v)
	case uint32:
		count = int64(v)
	case uint64:
		if v > uint64(MaxTokenCount) {
			return nil
		}
		count = int64(v)
	case float32:
		return tokenCount(float64(v))
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > float64(MaxTokenCount) || math.Trunc(v) != v {
			return nil
		}
		count = int64(v)
	case json.Number:
		// Bound work before parsing exact decimal/exponent values. A counter
		// cannot need an exponent outside this range within the length bound.
		s := string(v)
		if len(s) > 64 || !json.Valid([]byte(s)) {
			return nil
		}
		if at := strings.IndexAny(s, "eE"); at >= 0 {
			exponent, err := strconv.ParseInt(s[at+1:], 10, 16)
			if err != nil || exponent < -64 || exponent > 64 {
				return nil
			}
		}
		rational, ok := new(big.Rat).SetString(s)
		if !ok || !rational.IsInt() || !rational.Num().IsInt64() {
			return nil
		}
		count = rational.Num().Int64()
	default:
		return nil
	}
	if count < 0 || count > MaxTokenCount {
		return nil
	}
	return &count
}

func sanitizeTokenUsage(usage *TokenUsage) *TokenUsage {
	if usage == nil || (usage.Source != UsageSourceUpstream && usage.Source != UsageSourceEstimated) {
		return nil
	}
	copyCount := func(value *int64) *int64 {
		if value == nil || *value < 0 || *value > MaxTokenCount {
			return nil
		}
		count := *value
		return &count
	}
	out := &TokenUsage{
		Source:           usage.Source,
		PromptTokens:     copyCount(usage.PromptTokens),
		CompletionTokens: copyCount(usage.CompletionTokens),
		TotalTokens:      copyCount(usage.TotalTokens),
	}
	if out.PromptTokens == nil && out.CompletionTokens == nil && out.TotalTokens == nil {
		return nil
	}
	return out
}

// UsageTotals reports sums together with coverage; unknown counters contribute
// no invented zero. Saturation is explicit rather than overflowing JSON numbers.
type UsageTotals struct {
	Requests          int   `json:"requests"`
	WithUsage         int   `json:"with_usage"`
	UpstreamRequests  int   `json:"upstream_requests"`
	EstimatedRequests int   `json:"estimated_requests"`
	UnknownRequests   int   `json:"unknown_requests"`
	PromptTokens      int64 `json:"prompt_tokens"`
	CompletionTokens  int64 `json:"completion_tokens"`
	TotalTokens       int64 `json:"total_tokens"`
	PromptKnown       int   `json:"prompt_known"`
	CompletionKnown   int   `json:"completion_known"`
	TotalKnown        int   `json:"total_known"`
	Overflowed        bool  `json:"overflowed"`
}

type UsageGroup struct {
	Key string `json:"key"`
	UsageTotals
}

// UsageSummary describes only the matching retained recent window, never an
// all-time ledger or a sum of the pages a client happened to load.
type UsageSummary struct {
	Scope             string       `json:"scope"`
	Window            int          `json:"window"`
	Disabled          bool         `json:"disabled"`
	From              string       `json:"from"`
	To                string       `json:"to"`
	ByAccount         []UsageGroup `json:"by_account"`
	ByModel           []UsageGroup `json:"by_model"`
	AccountsTruncated int          `json:"accounts_truncated"`
	ModelsTruncated   int          `json:"models_truncated"`
	UsageTotals
}

func emptyUsageSummary(window int, disabled bool) UsageSummary {
	return UsageSummary{Scope: "recent_window", Window: window, Disabled: disabled,
		ByAccount: []UsageGroup{}, ByModel: []UsageGroup{}}
}

func (totals *UsageTotals) add(usage *TokenUsage) {
	totals.Requests++
	if usage == nil {
		totals.UnknownRequests++
		return
	}
	totals.WithUsage++
	if usage.Source == UsageSourceUpstream {
		totals.UpstreamRequests++
	} else {
		totals.EstimatedRequests++
	}
	add := func(sum *int64, known *int, value *int64) {
		if value == nil {
			return
		}
		*known = *known + 1
		if *value > MaxTokenCount-*sum {
			*sum = MaxTokenCount
			totals.Overflowed = true
			return
		}
		*sum += *value
	}
	add(&totals.PromptTokens, &totals.PromptKnown, usage.PromptTokens)
	add(&totals.CompletionTokens, &totals.CompletionKnown, usage.CompletionTokens)
	add(&totals.TotalTokens, &totals.TotalKnown, usage.TotalTokens)
}

func summarizeUsage(events []Event, window int, disabled bool) UsageSummary {
	summary := emptyUsageSummary(window, disabled)
	if disabled {
		return summary
	}
	// Both maps are bounded by the already-clamped recent event count (8192).
	accounts := make(map[string]*UsageTotals)
	models := make(map[string]*UsageTotals)
	addGroup := func(groups map[string]*UsageTotals, key string, usage *TokenUsage) {
		if groups[key] == nil {
			groups[key] = &UsageTotals{}
		}
		groups[key].add(usage)
	}
	var from, to time.Time
	for _, event := range events {
		summary.add(event.Usage)
		if ts, err := time.Parse(time.RFC3339Nano, event.TS); err == nil {
			if from.IsZero() || ts.Before(from) {
				from = ts
			}
			if to.IsZero() || ts.After(to) {
				to = ts
			}
		}
		account := ""
		if event.Account != nil {
			account = *event.Account
		}
		model := event.ModelCanonical
		if model == "" {
			model = event.Model
		}
		addGroup(accounts, account, event.Usage)
		addGroup(models, model, event.Usage)
	}
	if !from.IsZero() {
		summary.From = from.UTC().Format(time.RFC3339Nano)
		summary.To = to.UTC().Format(time.RFC3339Nano)
	}
	summary.ByAccount, summary.AccountsTruncated = usageGroups(accounts)
	summary.ByModel, summary.ModelsTruncated = usageGroups(models)
	return summary
}

func usageGroups(groups map[string]*UsageTotals) ([]UsageGroup, int) {
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	truncated := 0
	if len(keys) > MaxUsageGroups {
		truncated = len(keys) - MaxUsageGroups
		keys = keys[:MaxUsageGroups]
	}
	out := make([]UsageGroup, 0, len(keys))
	for _, key := range keys {
		out = append(out, UsageGroup{Key: key, UsageTotals: *groups[key]})
	}
	return out, truncated
}
