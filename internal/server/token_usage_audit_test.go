package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"codearts2api/internal/audit"
)

const tokenAuditAnswer = "data: {\"choices\":[{\"delta\":{\"content\":\"OK\",\"reasoning_content\":\"thinking\"},\"finish_reason\":\"stop\"}]}\n\n"
const tokenAuditDone = "data: [DONE]\n\n"

func tokenAuditWire(usage string) string {
	if usage == "" {
		return tokenAuditAnswer + tokenAuditDone
	}
	return tokenAuditAnswer + "data: {\"choices\":[],\"usage\":" + usage + "}\n\n" + tokenAuditDone
}

func tokenAuditBackend(t *testing.T, wire string) *httptest.Server {
	t.Helper()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, wire)
	}))
	t.Cleanup(backend.Close)
	return backend
}

func tokenAuditRequest(t *testing.T, frontend *httptest.Server, method, path, body string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, frontend.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer token-audit-test-key")
	client := &http.Client{Timeout: 2 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res, raw
}

func tokenAuditChatBody(stream bool, options string) string {
	streamValue := "false"
	if stream {
		streamValue = "true"
	}
	return `{"model":"glm-5.2","messages":[{"role":"user","content":"hi"}],"stream":` + streamValue + options + `}`
}

func TestTokenAuditCapturesSuccessfulHTTPUsageWithoutChangingClientOptIn(t *testing.T) {
	for _, tc := range []struct {
		name, native, options, source string
		stream                        bool
		prompt, completion, total     *int64
	}{
		{"nonstream-native", `{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30,"provider_note":"usage-private-metadata"}`, "", "upstream", false, tokenAuditCount(10), tokenAuditCount(20), tokenAuditCount(30)},
		{"nonstream-estimate", "", "", "estimated", false, tokenAuditCount(1), tokenAuditCount(3), tokenAuditCount(4)},
		{"stream-native-omitted", `{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30}`, "", "upstream", true, tokenAuditCount(10), tokenAuditCount(20), tokenAuditCount(30)},
		{"stream-native-disabled", `{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30}`, `,"stream_options":{"include_usage":false}`, "upstream", true, tokenAuditCount(10), tokenAuditCount(20), tokenAuditCount(30)},
		{"stream-native-enabled", `{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30}`, `,"stream_options":{"include_usage":true}`, "upstream", true, tokenAuditCount(10), tokenAuditCount(20), tokenAuditCount(30)},
		{"stream-estimate-omitted", "", "", "estimated", true, tokenAuditCount(1), tokenAuditCount(3), tokenAuditCount(4)},
		{"stream-native-zero", `{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}`, "", "upstream", true, tokenAuditCount(0), tokenAuditCount(0), tokenAuditCount(0)},
		{"nonstream-native-partial", `{"completion_tokens":7}`, "", "upstream", false, nil, tokenAuditCount(7), nil},
		{"stream-native-derived-total", `{"prompt_tokens":10,"completion_tokens":20}`, "", "upstream", true, tokenAuditCount(10), tokenAuditCount(20), tokenAuditCount(30)},
		{"stream-native-invalid-stays-unknown", `{"prompt_tokens":-1,"completion_tokens":"usage-invalid-private","total_tokens":0.25}`, "", "", true, nil, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := tokenAuditBackend(t, tokenAuditWire(tc.native))
			sink := newAuditSink(t)
			h, p := auditHandler(t, backend.URL, 1, sink, func(c *Config) { c.APIKey = "token-audit-test-key" })
			frontend := httptest.NewServer(h)
			defer frontend.Close()
			res, raw := tokenAuditRequest(t, frontend, http.MethodPost, "/v1/chat/completions", tokenAuditChatBody(tc.stream, tc.options))
			if res.StatusCode != http.StatusOK {
				t.Fatalf("chat status=%d body=%s", res.StatusCode, raw)
			}
			if tc.stream {
				if !strings.HasSuffix(strings.TrimSpace(string(raw)), "data: [DONE]") {
					t.Fatalf("missing successful DONE: %s", raw)
				}
				if tc.options == "" || strings.Contains(tc.options, "false") {
					if strings.Contains(string(raw), `"usage"`) {
						t.Fatalf("audit collection changed public include_usage opt-in: %s", raw)
					}
				} else if !strings.Contains(string(raw), `"total_tokens":30`) {
					t.Fatalf("requested public usage missing: %s", raw)
				}
			}
			ev := waitForRecord(t, sink, res.Header.Get("X-Request-Id"))
			if ev.Outcome != "ok" {
				t.Fatalf("generation did not complete successfully: %+v", ev)
			}
			if tc.source == "" && ev.Usage != nil {
				t.Fatalf("invalid native counters became an estimate/exact usage: %+v", ev.Usage)
			}
			if tc.source != "" && (ev.Usage == nil || ev.Usage.Source != tc.source) {
				t.Fatalf("successful generation usage missing/source wrong: %+v", ev)
			}
			if ev.Usage != nil {
				assertTokenAuditCount(t, "prompt", ev.Usage.PromptTokens, tc.prompt)
				assertTokenAuditCount(t, "completion", ev.Usage.CompletionTokens, tc.completion)
				assertTokenAuditCount(t, "total", ev.Usage.TotalTokens, tc.total)
			}
			_, pageRaw := tokenAuditRequest(t, frontend, http.MethodGet, "/admin/api/requests?limit=1", "")
			var page struct {
				Events []audit.Event `json:"events"`
				Usage  struct {
					Scope             string `json:"scope"`
					Requests          int    `json:"requests"`
					WithUsage         int    `json:"with_usage"`
					UpstreamRequests  int    `json:"upstream_requests"`
					EstimatedRequests int    `json:"estimated_requests"`
					TotalKnown        int    `json:"total_known"`
					UnknownRequests   int    `json:"unknown_requests"`
				} `json:"usage"`
			}
			if err := json.Unmarshal(pageRaw, &page); err != nil {
				t.Fatal(err)
			}
			if len(page.Events) != 1 || page.Events[0].ID != ev.ID || page.Usage.Scope != "recent_window" || page.Usage.Requests != 1 {
				t.Fatalf("actual requests API dropped correlated usage: %s", pageRaw)
			}
			if tc.source == "" {
				if page.Events[0].Usage != nil || page.Usage.WithUsage != 0 || page.Usage.UnknownRequests != 1 {
					t.Fatalf("API fabricated unknown native usage: %s", pageRaw)
				}
			} else if page.Events[0].Usage == nil || page.Usage.WithUsage != 1 || page.Usage.UnknownRequests != 0 {
				t.Fatalf("API dropped known usage: %s", pageRaw)
			}
			if (tc.source == "upstream" && page.Usage.UpstreamRequests != 1) || (tc.source == "estimated" && page.Usage.EstimatedRequests != 1) {
				t.Fatalf("summary fabricated usage provenance: %s", pageRaw)
			}
			if (tc.total == nil && page.Usage.TotalKnown != 0) || (tc.total != nil && page.Usage.TotalKnown != 1) {
				t.Fatalf("summary confused missing total with zero: %s", pageRaw)
			}
			if strings.Contains(string(pageRaw), "usage-private-metadata") || strings.Contains(string(pageRaw), "usage-invalid-private") {
				t.Fatal("arbitrary provider usage metadata reached audit API")
			}
			assertLifecycleHealthy(t, p)
		})
	}
}

func tokenAuditCount(value int64) *int64 { return &value }

func assertTokenAuditCount(t *testing.T, name string, actual, expected *int64) {
	t.Helper()
	if expected == nil {
		if actual != nil {
			t.Errorf("%s must remain unknown; got %d", name, *actual)
		}
		return
	}
	if actual == nil || *actual != *expected {
		t.Errorf("%s token mismatch: got %v, expected %d", name, actual, *expected)
	}
}

func TestTokenAuditDoesNotInventSuccessfulUsageForFailedGeneration(t *testing.T) {
	const failed = tokenAuditAnswer + "data: {\"choices\":[],\"usage\":{\"completion_tokens\":7}}\n\n" + "event: error\ndata: {\"code\":\"InferHub.4004.200\",\"message\":\"benefit not found\"}\n\n"
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "nonstream", true: "stream"}[stream], func(t *testing.T) {
			backend := tokenAuditBackend(t, failed)
			sink := newAuditSink(t)
			h, p := auditHandler(t, backend.URL, 1, sink, func(c *Config) { c.APIKey = "token-audit-test-key" })
			frontend := httptest.NewServer(h)
			defer frontend.Close()
			res, _ := tokenAuditRequest(t, frontend, http.MethodPost, "/v1/chat/completions", tokenAuditChatBody(stream, `,"stream_options":{"include_usage":true}`))
			ev := waitForRecord(t, sink, res.Header.Get("X-Request-Id"))
			if ev.Outcome == "ok" || ev.Usage != nil {
				t.Fatalf("partial failed generation must retain unknown usage: %+v", ev)
			}
			assertLifecycleHealthy(t, p)
		})
	}
}

type tokenAuditFailDoneWriter struct{ header http.Header }

func (w *tokenAuditFailDoneWriter) Header() http.Header { return w.header }
func (w *tokenAuditFailDoneWriter) WriteHeader(int)     {}
func (w *tokenAuditFailDoneWriter) Flush()              {}
func (w *tokenAuditFailDoneWriter) Write(data []byte) (int, error) {
	if strings.Contains(string(data), "data: [DONE]") {
		return 0, errors.New("synthetic downstream DONE failure")
	}
	return len(data), nil
}

func TestTokenAuditFailedTerminalDeliveryRemainsUnknown(t *testing.T) {
	backend := tokenAuditBackend(t, tokenAuditWire(`{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30}`))
	sink := newAuditSink(t)
	h, p := auditHandler(t, backend.URL, 1, sink, nil)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(tokenAuditChatBody(true, `,"stream_options":{"include_usage":true}`)))
	w := &tokenAuditFailDoneWriter{header: make(http.Header)}
	h.ServeHTTP(w, req)
	ev := waitForRecord(t, sink, w.Header().Get("X-Request-Id"))
	if ev.Outcome != "downstream_canceled" || ev.Usage != nil {
		t.Fatalf("usage emitted before failed DONE must not become delivered-success usage: %+v", ev)
	}
	assertLifecycleHealthy(t, p)
}

func TestTokenAuditCanceledAndUnavailableRequestsRemainUnknown(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "no-account", true: "canceled-before-attempt"}[canceled], func(t *testing.T) {
			sink := newAuditSink(t)
			h, p := auditHandler(t, "http://127.0.0.1:1", 0, sink, nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if canceled {
				cancel()
			}
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(tokenAuditChatBody(true, ""))).WithContext(ctx)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			ev := waitForRecord(t, sink, rec.Header().Get("X-Request-Id"))
			if ev.Outcome == "ok" || ev.Usage != nil || ev.Attempts != 0 {
				t.Fatalf("unattempted request invented generation usage: %+v", ev)
			}
			assertLifecycleHealthy(t, p)
		})
	}
}

func TestTokenAuditAPIReplaysUsageAndKeepsFilteredSummaryIndependentOfPages(t *testing.T) {
	const native = `{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30,"provider_note":"usage-private-metadata"}`
	backend := tokenAuditBackend(t, tokenAuditWire(native))
	sink := newAuditSink(t)
	options := sink.Options()
	h, _ := auditHandler(t, backend.URL, 1, sink, func(c *Config) { c.APIKey = "token-audit-test-key" })
	frontend := httptest.NewServer(h)
	defer frontend.Close()
	var first audit.Event
	for i := 0; i < 2; i++ {
		res, _ := tokenAuditRequest(t, frontend, http.MethodPost, "/v1/chat/completions", tokenAuditChatBody(i == 1, ""))
		first = waitForRecord(t, sink, res.Header.Get("X-Request-Id"))
	}
	sink.Record(audit.Event{ID: "legacy-token-audit-record", TS: time.Now().UTC().Format(time.RFC3339Nano), Model: "legacy-other-model", Outcome: "ok", Committed: true, Status: 200})
	sink.Close(context.Background())
	files, err := filepath.Glob(filepath.Join(options.Dir, "*.jsonl"))
	if err != nil || len(files) == 0 {
		t.Fatalf("audit persistence unavailable: %v", err)
	}
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), `"prompt_tokens":10`) || strings.Contains(string(raw), "usage-private-metadata") {
			t.Fatalf("safe typed usage not persisted correctly: %s", raw)
		}
	}
	reopened, err := audit.Open(options)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close(context.Background())
	h.cfg.Audit = reopened
	var firstPage struct {
		Events     []audit.Event   `json:"events"`
		NextBefore uint64          `json:"next_before"`
		Usage      json.RawMessage `json:"usage"`
	}
	query := "/admin/api/requests?limit=1&model=" + url.QueryEscape(first.ModelCanonical) + "&account=" + url.QueryEscape(*first.Account)
	_, raw := tokenAuditRequest(t, frontend, http.MethodGet, query, "")
	if err := json.Unmarshal(raw, &firstPage); err != nil {
		t.Fatal(err)
	}
	if len(firstPage.Events) != 1 || firstPage.Events[0].Usage == nil {
		t.Fatalf("replayed per-request usage missing: %s", raw)
	}
	var summary struct {
		Requests        int                    `json:"requests"`
		TotalTokens     int64                  `json:"total_tokens"`
		UnknownRequests int                    `json:"unknown_requests"`
		ByAccount       []struct{ Key string } `json:"by_account"`
		ByModel         []struct{ Key string } `json:"by_model"`
	}
	if err := json.Unmarshal(firstPage.Usage, &summary); err != nil {
		t.Fatal(err)
	}
	if summary.Requests != 2 || summary.TotalTokens != 60 || summary.UnknownRequests != 0 || len(summary.ByAccount) != 1 || summary.ByAccount[0].Key != *first.Account || len(summary.ByModel) != 1 || summary.ByModel[0].Key != first.ModelCanonical {
		t.Fatalf("filtered summary lost replayed/request identity or included unrelated legacy record: %s", raw)
	}
	_, raw = tokenAuditRequest(t, frontend, http.MethodGet, query+"&before="+strconv.FormatUint(firstPage.NextBefore, 10), "")
	var secondPage struct {
		Events []audit.Event   `json:"events"`
		Usage  json.RawMessage `json:"usage"`
	}
	if err := json.Unmarshal(raw, &secondPage); err != nil {
		t.Fatal(err)
	}
	if len(secondPage.Events) != 1 || string(secondPage.Usage) != string(firstPage.Usage) {
		t.Fatalf("pagination changed total usage instead of only paging requests: first=%s second=%s", firstPage.Usage, raw)
	}
	_, raw = tokenAuditRequest(t, frontend, http.MethodGet, "/admin/api/audit", "")
	var auditPage struct {
		Usage struct {
			Requests        int   `json:"requests"`
			TotalTokens     int64 `json:"total_tokens"`
			UnknownRequests int   `json:"unknown_requests"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &auditPage); err != nil {
		t.Fatal(err)
	}
	if auditPage.Usage.Requests != 3 || auditPage.Usage.TotalTokens != 60 || auditPage.Usage.UnknownRequests != 1 {
		t.Fatalf("audit summary fabricated historical usage: %s", raw)
	}
}

func TestTokenAuditDisabledAPIsExposeHonestEmptyUsage(t *testing.T) {
	for _, disabledSink := range []bool{false, true} {
		t.Run(map[bool]string{false: "nil", true: "configured-disabled"}[disabledSink], func(t *testing.T) {
			var sink *audit.Sink
			if disabledSink {
				var err error
				sink, err = audit.Open(audit.Options{Enabled: false})
				if err != nil {
					t.Fatal(err)
				}
				defer sink.Close(context.Background())
			}
			h, _ := auditHandler(t, "http://127.0.0.1:1", 0, sink, func(c *Config) { c.APIKey = "token-audit-test-key" })
			frontend := httptest.NewServer(h)
			defer frontend.Close()
			for _, path := range []string{"/admin/api/requests", "/admin/api/audit"} {
				res, raw := tokenAuditRequest(t, frontend, http.MethodGet, path, "")
				var data struct {
					Usage struct {
						Scope    string `json:"scope"`
						Disabled bool   `json:"disabled"`
						Requests int    `json:"requests"`
						ByModel  []any  `json:"by_model"`
					} `json:"usage"`
				}
				if err := json.Unmarshal(raw, &data); err != nil {
					t.Fatal(err)
				}
				if res.StatusCode != http.StatusOK || data.Usage.Scope != "recent_window" || !data.Usage.Disabled || data.Usage.Requests != 0 || data.Usage.ByModel == nil {
					t.Fatalf("disabled usage must be explicit empty bounded scope: status=%d body=%s", res.StatusCode, raw)
				}
			}
		})
	}
}
