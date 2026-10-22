package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"codearts2api/internal/audit"
	"codearts2api/internal/pool"
	"codearts2api/internal/upstream"
)

const concurrencyErrorSSE = "data: {\"error_code\":\"TM.00001041\",\"error_msg\":\"concurrent session limit\"}\n\n"

type concurrencyFixture struct {
	h     *Handler
	p     *pool.Pool
	sink  *audit.Sink
	mu    sync.Mutex
	calls []int
}

func newConcurrencyFixture(t *testing.T, accounts int, respond func(int, http.ResponseWriter)) *concurrencyFixture {
	t.Helper()
	f := &concurrencyFixture{sink: newAuditSink(t)}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		index, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/"))
		if err != nil || index < 0 || index >= accounts {
			t.Errorf("unexpected upstream path: %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		f.mu.Lock()
		f.calls = append(f.calls, index)
		f.mu.Unlock()
		respond(index, w)
	}))
	t.Cleanup(backend.Close)
	f.h, f.p = auditHandler(t, backend.URL, accounts, f.sink, func(c *Config) {
		c.QueueMaxAttempts = 3
		c.QueueRetryDelay = time.Millisecond
		c.StartupTimeout = 3 * time.Second
	})
	for index, account := range f.p.Accounts() {
		account.Client = upstream.NewWithChatEndpoint(time.Second, backend.URL+"/"+strconv.Itoa(index))
	}
	return f
}

func (f *concurrencyFixture) assertAttempts(t *testing.T, id string, want []int) audit.Event {
	t.Helper()
	f.mu.Lock()
	got := append([]int(nil), f.calls...)
	f.mu.Unlock()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("actual upstream account sequence = %v, want %v", got, want)
	}
	ev := waitForRecord(t, f.sink, id)
	var tried []string
	seen := make(map[int]bool)
	accounts := f.p.Accounts()
	for _, index := range want {
		if !seen[index] {
			tried = append(tried, accounts[index].Name)
			seen[index] = true
		}
	}
	if ev.Attempts != len(got) || !reflect.DeepEqual(ev.Tried, tried) || ev.Account == nil || *ev.Account != accounts[want[len(want)-1]].Name {
		t.Fatalf("audit does not match actual upstream attempts: %+v; want tried=%v", ev, tried)
	}
	if active := f.sink.Connections().Active; len(active) != 0 {
		t.Fatalf("completed request retained active tracking: %+v", active)
	}
	return ev
}

func (f *concurrencyFixture) assertNeutral(t *testing.T) {
	t.Helper()
	assertLifecycleHealthy(t, f.p)
	for _, account := range f.p.Accounts() {
		if state, reason := modelAvailability(account.UID, "glm-5.2"); state == availUnusable {
			t.Fatalf("concurrency refusal poisoned model capability: %s %q", account.Name, reason)
		}
	}
	// Reacquire the entire admission capacity so a leaked lifecycle ticket is
	// observable even when a later request would fit within the spare capacity.
	capacity := f.p.Config().MaxPending + len(f.p.Accounts())
	for i := 0; i < capacity; i++ {
		release, err := f.p.AdmitRequest(context.Background())
		if err != nil {
			t.Fatalf("admission capacity leaked: acquired %d/%d: %v", i, capacity, err)
		}
		defer release()
	}
}

func writeConcurrencyRefusal(w http.ResponseWriter, status int) {
	if status == http.StatusOK {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, concurrencyErrorSSE)
		return
	}
	w.WriteHeader(status)
	_, _ = io.WriteString(w, `{"error":"TM.00001041 concurrent session limit"}`)
}

func TestChatConcurrentLimitRotatesBeforeRetryDelay(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
	}{{"http400", http.StatusBadRequest}, {"http429", http.StatusTooManyRequests}, {"first_sse", http.StatusOK}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newConcurrencyFixture(t, 2, func(index int, w http.ResponseWriter) {
				if index == 0 {
					writeConcurrencyRefusal(w, tc.status)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, lifecycleSuccess)
			})
			// Sleeping on A would exhaust startup before B could ever be tried.
			f.h.cfg.QueueRetryDelay = 5 * time.Second
			f.h.cfg.StartupTimeout = 500 * time.Millisecond
			rec, id := doChat(f.h, "glm-5.2")
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"content":"ok"`) {
				t.Fatalf("healthy backup did not bypass retry delay: %d %s", rec.Code, rec.Body.String())
			}
			ev := f.assertAttempts(t, id, []int{0, 1})
			if ev.Outcome != "ok" || ev.Cause.Class != audit.CauseNone || ev.Status != http.StatusOK {
				t.Fatalf("recovered request has incorrect audit result: %+v", ev)
			}
			f.assertNeutral(t)
		})
	}
}

func TestChatConcurrentLimitRetryBudgets(t *testing.T) {
	for _, tc := range []struct {
		name              string
		accounts, rotates int
		want              []int
	}{
		{"single_account", 1, 1, []int{0, 0, 0}},
		{"all_busy", 3, 3, []int{0, 1, 2, 2, 2}},
		{"rotation_exhausted", 2, 1, []int{0, 0, 0}},
		{"untried_account", 4, 3, []int{0, 1, 2, 2, 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newConcurrencyFixture(t, tc.accounts, func(_ int, w http.ResponseWriter) {
				writeConcurrencyRefusal(w, http.StatusBadRequest)
			})
			f.h.cfg.MaxRotate = tc.rotates
			rec, id := doChat(f.h, "glm-5.2")
			if rec.Code != http.StatusTooManyRequests || !strings.Contains(rec.Body.String(), `"code":"upstream_busy"`) || rec.Header().Get("Retry-After") != "2" {
				t.Fatalf("concurrency exhaustion = %d %s, Retry-After=%q", rec.Code, rec.Body.String(), rec.Header().Get("Retry-After"))
			}
			ev := f.assertAttempts(t, id, tc.want)
			if ev.Outcome != "upstream_saturated" || ev.Cause.Class != audit.CauseConcurrencyLimit || ev.Cause.Code != "TM.00001041" || ev.ErrorCode != "upstream_busy" || ev.Status != rec.Code || !ev.Committed || ev.RetryAfterS != 2 {
				t.Fatalf("concurrency exhaustion audit = %+v", ev)
			}
			f.assertNeutral(t)
		})
	}
}

func TestChatConcurrentLimitPreservesRateRetryPolicy(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		body     string
		errCount int
	}{
		{"http429", http.StatusTooManyRequests, `{"error":"rate limit reached"}`, 0},
		{"http400_tpm", http.StatusBadRequest, `{"error":"InferHub.ModelArts.81111.429 TPM limit reached"}`, 1},
		{"first_sse_tpm", http.StatusOK, "data: {\"error_code\":\"InferHub.ModelArts.81111.429\",\"error_msg\":\"TPM limit reached\"}\n\n", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newConcurrencyFixture(t, 2, func(index int, w http.ResponseWriter) {
				w.Header().Set("Content-Type", "text/event-stream")
				if index == 0 {
					w.WriteHeader(tc.status)
					_, _ = io.WriteString(w, tc.body)
					return
				}
				_, _ = io.WriteString(w, lifecycleSuccess)
			})
			rec, id := doChat(f.h, "glm-5.2")
			if rec.Code != http.StatusOK {
				t.Fatalf("rate retry did not reach backup: %d %s", rec.Code, rec.Body.String())
			}
			f.assertAttempts(t, id, []int{0, 0, 0, 1})
			states := f.p.List()
			if states[0]["cooling"] != true || states[0]["disabled"] != false || states[0]["err_count"] != tc.errCount || states[0]["active_concurrent"] != 0 {
				t.Fatalf("ordinary rate policy changed: %#v", states[0])
			}
			if !f.p.Healthy(f.p.Accounts()[1].Name) || states[1]["active_concurrent"] != 0 {
				t.Fatalf("backup was punished or retained: %#v", states[1])
			}
		})
	}
}

func TestChatConcurrentLimitAfterFirstDataDoesNotRetry(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(strconv.FormatBool(stream), func(t *testing.T) {
			f := newConcurrencyFixture(t, 2, func(index int, w http.ResponseWriter) {
				w.Header().Set("Content-Type", "text/event-stream")
				if index == 0 {
					_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"+concurrencyErrorSSE)
					return
				}
				_, _ = io.WriteString(w, lifecycleSuccess)
			})
			rec := httptest.NewRecorder()
			f.h.ServeHTTP(rec, lifecycleRequest(context.Background(), stream))
			wantStatus, wantOutcome := http.StatusBadGateway, "upstream_body_error"
			if stream {
				wantStatus, wantOutcome = http.StatusOK, "upstream_stream_error"
				if !strings.Contains(rec.Body.String(), "partial") || !strings.Contains(rec.Body.String(), "data: [DONE]") || !strings.Contains(rec.Body.String(), `"error"`) {
					t.Fatalf("late refusal did not terminate the partial stream: %s", rec.Body.String())
				}
			}
			if rec.Code != wantStatus {
				t.Fatalf("late refusal status=%d want=%d body=%s", rec.Code, wantStatus, rec.Body.String())
			}
			ev := f.assertAttempts(t, rec.Header().Get("X-Request-Id"), []int{0})
			if ev.Outcome != wantOutcome || ev.Cause.Class != audit.CauseConcurrencyLimit || ev.Cause.Code != "TM.00001041" || ev.Status != wantStatus || !ev.Committed || ev.Stream != stream || ev.FirstDataMS == nil {
				t.Fatalf("late concurrency audit = %+v", ev)
			}
			f.assertNeutral(t)
		})
	}
}
