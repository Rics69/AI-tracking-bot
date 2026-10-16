package gigachat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/llm"
)

// fakeGigaChat serves /oauth and /chat/completions.
type fakeGigaChat struct {
	t          *testing.T
	tokens     atomic.Int32 // tokens issued
	chats      atomic.Int32 // chat requests received
	lastChat   chatRequest
	lastHeader http.Header
	// chat returns the status and body for the n-th chat request (1-based).
	chat func(n int32) (int, string)
}

func (f *fakeGigaChat) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/oauth":
		if r.Header.Get("Authorization") != "Basic test-key" || r.Header.Get("RqUID") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != "scope=GIGACHAT_API_PERS" {
			f.t.Errorf("oauth body = %q", body)
		}
		n := f.tokens.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "token-" + string(rune('0'+n)),
			"expires_at":   time.Now().Add(30 * time.Minute).UnixMilli(),
		})
	case "/api/v1/chat/completions":
		f.lastHeader = r.Header.Clone()
		if err := json.NewDecoder(r.Body).Decode(&f.lastChat); err != nil {
			f.t.Errorf("decode chat request: %v", err)
		}
		status, body := f.chat(f.chats.Add(1))
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	default:
		http.NotFound(w, r)
	}
}

func newTestClient(t *testing.T, chat func(n int32) (int, string)) (*Client, *fakeGigaChat) {
	t.Helper()
	fake := &fakeGigaChat{t: t, chat: chat}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	c, err := New(Config{
		AuthKey:    "test-key",
		BaseURL:    srv.URL + "/api/v1",
		AuthURL:    srv.URL + "/oauth",
		MaxRetries: 2,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	c.sleep = func(context.Context, time.Duration) error { return nil }
	return c, fake
}

const textAnswer = `{"model":"GigaChat-2-Pro","choices":[{"message":{"role":"assistant","content":"Привет!"},"finish_reason":"stop"}],
 "usage":{"prompt_tokens":10,"completion_tokens":3,"precached_prompt_tokens":4,"total_tokens":9}}`

func ok(body string) func(int32) (int, string) {
	return func(int32) (int, string) { return 200, body }
}

func TestChatTextAnswer(t *testing.T) {
	c, fake := newTestClient(t, ok(textAnswer))
	resp, err := c.Chat(context.Background(), llm.Request{
		Messages:  []llm.Message{{Role: llm.RoleSystem, Content: "sys"}, {Role: llm.RoleUser, Content: "привет"}},
		SessionID: "tg-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message.Content != "Привет!" || resp.Message.ToolCall != nil {
		t.Errorf("message = %+v", resp.Message)
	}
	if resp.Usage.PromptTokens != 10 || resp.Usage.CachedTokens != 4 {
		t.Errorf("usage = %+v", resp.Usage)
	}
	if got := fake.lastHeader.Get("Authorization"); got != "Bearer token-1" {
		t.Errorf("Authorization = %q", got)
	}
	if fake.lastHeader.Get("X-Session-ID") != "tg-1" {
		t.Errorf("X-Session-ID not sent")
	}
	if fake.lastChat.Model != DefaultModel || len(fake.lastChat.Functions) != 0 || fake.lastChat.FunctionCall != "" {
		t.Errorf("request = %+v", fake.lastChat)
	}
}

func TestTokenIsCachedAndRefreshedOn401(t *testing.T) {
	c, fake := newTestClient(t, func(n int32) (int, string) {
		if n == 3 {
			return http.StatusUnauthorized, `{"message":"token expired"}`
		}
		return 200, textAnswer
	})
	ctx := context.Background()
	for range 2 {
		if _, err := c.Chat(ctx, llm.Request{}); err != nil {
			t.Fatal(err)
		}
	}
	if fake.tokens.Load() != 1 {
		t.Fatalf("token fetched %d times for 2 requests, want 1", fake.tokens.Load())
	}
	// Third request gets 401: the client fetches a new token and repeats it.
	if _, err := c.Chat(ctx, llm.Request{}); err != nil {
		t.Fatal(err)
	}
	if fake.tokens.Load() != 2 || fake.chats.Load() != 4 {
		t.Errorf("tokens = %d chats = %d, want 2 and 4", fake.tokens.Load(), fake.chats.Load())
	}
}

func TestRetriesOnRateLimit(t *testing.T) {
	c, fake := newTestClient(t, func(n int32) (int, string) {
		if n <= 2 {
			return http.StatusTooManyRequests, `{"message":"too many requests"}`
		}
		return 200, textAnswer
	})
	if _, err := c.Chat(context.Background(), llm.Request{}); err != nil {
		t.Fatal(err)
	}
	if fake.chats.Load() != 3 {
		t.Errorf("chat requests = %d, want 3", fake.chats.Load())
	}
}

func TestGivesUpAfterMaxRetries(t *testing.T) {
	c, _ := newTestClient(t, func(int32) (int, string) { return 503, `unavailable` })
	_, err := c.Chat(context.Background(), llm.Request{})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 503 {
		t.Fatalf("err = %v, want APIError 503", err)
	}
}

func TestFunctionCallRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		args string
	}{
		{"arguments as object", `{"amount":350,"category":"food"}`},
		{"arguments as string", `"{\"amount\":350,\"category\":\"food\"}"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			answer := `{"choices":[{"message":{"role":"assistant","content":"","functions_state_id":"state-1",
				"function_call":{"name":"add_expense","arguments":` + tt.args + `}},"finish_reason":"function_call"}]}`
			c, fake := newTestClient(t, ok(answer))
			c.cfg.Temperature = new(0.1)

			resp, err := c.Chat(context.Background(), llm.Request{
				Messages: []llm.Message{{Role: llm.RoleUser, Content: "кофе 350"}},
				Tools: []llm.Tool{{
					Name: "add_expense", Description: "Добавить трату",
					Parameters: json.RawMessage(`{"type":"object"}`),
					Examples:   []llm.ToolExample{{Request: "кофе 350", Params: map[string]any{"amount": 350}}},
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			call := resp.Message.ToolCall
			if call == nil || call.Name != "add_expense" || resp.Message.StateID != "state-1" {
				t.Fatalf("message = %+v", resp.Message)
			}
			var args map[string]any
			if err := json.Unmarshal(call.Arguments, &args); err != nil || args["amount"] != 350.0 {
				t.Errorf("arguments = %s", call.Arguments)
			}

			req := fake.lastChat
			if req.FunctionCall != "auto" || len(req.Functions) != 1 ||
				len(req.Functions[0].FewShotExamples) != 1 || req.Temperature == nil || *req.Temperature != 0.1 {
				t.Errorf("request = %+v", req)
			}
		})
	}
}

func TestHistoryIsSentInGigaChatFormat(t *testing.T) {
	c, fake := newTestClient(t, ok(textAnswer))
	_, err := c.Chat(context.Background(), llm.Request{Messages: []llm.Message{
		{Role: llm.RoleUser, Content: "кофе 350"},
		{Role: llm.RoleAssistant, StateID: "state-1",
			ToolCall: &llm.ToolCall{Name: "add_expense", Arguments: json.RawMessage(`{"amount":350}`)}},
		{Role: llm.RoleTool, ToolName: "add_expense", Content: `{"added":{"id":1}}`},
	}})
	if err != nil {
		t.Fatal(err)
	}
	msgs := fake.lastChat.Messages
	if len(msgs) != 3 {
		t.Fatalf("messages = %+v", msgs)
	}
	if a := msgs[1]; a.Role != "assistant" || a.FunctionsStateID != "state-1" ||
		a.FunctionCall == nil || string(a.FunctionCall.Arguments) != `{"amount":350}` {
		t.Errorf("assistant message = %+v", a)
	}
	if f := msgs[2]; f.Role != "function" || f.Name != "add_expense" || !strings.Contains(f.Content, `"added"`) {
		t.Errorf("function message = %+v", f)
	}
}

func TestBlacklistedAnswer(t *testing.T) {
	c, _ := newTestClient(t, ok(`{"choices":[{"message":{"role":"assistant","content":"..."},"finish_reason":"blacklist"}]}`))
	if _, err := c.Chat(context.Background(), llm.Request{}); !errors.Is(err, llm.ErrBlocked) {
		t.Errorf("err = %v, want ErrBlocked", err)
	}
}

func TestEmbeddedRootCAParses(t *testing.T) {
	if _, err := tlsConfig(false); err != nil {
		t.Fatal(err)
	}
}

func TestRetriesNetworkFailures(t *testing.T) {
	c, fake := newTestClient(t, ok(textAnswer))
	// Wrap the fake so the first chat request loses its connection mid-flight.
	var dropped atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/chat/completions" && dropped.CompareAndSwap(false, true) {
			conn, _, _ := w.(http.Hijacker).Hijack()
			_ = conn.Close()
			return
		}
		fake.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	c.cfg.BaseURL, c.cfg.AuthURL = srv.URL+"/api/v1", srv.URL+"/oauth"

	resp, err := c.Chat(context.Background(), llm.Request{})
	if err != nil {
		t.Fatalf("err = %v, want success after a retry", err)
	}
	if resp.Message.Content != "Привет!" || fake.chats.Load() != 1 {
		t.Errorf("content = %q, chats served = %d", resp.Message.Content, fake.chats.Load())
	}
}

func TestDoesNotRetryWhenCallerGaveUp(t *testing.T) {
	c, fake := newTestClient(t, func(int32) (int, string) { return 503, "unavailable" })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Chat(ctx, llm.Request{}); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if fake.chats.Load() != 0 {
		t.Errorf("sent %d requests after cancellation", fake.chats.Load())
	}
}

func TestOutcomeClassification(t *testing.T) {
	for err, want := range map[error]string{
		nil:                            llm.OutcomeOK,
		llm.ErrBlocked:                 llm.OutcomeBlocked,
		&APIError{Status: 429}:         llm.OutcomeRateLimited,
		&APIError{Status: 503}:         llm.OutcomeServerError,
		&APIError{Status: 400}:         llm.OutcomeClientError,
		context.DeadlineExceeded:       llm.OutcomeNetwork,
		errors.New("connection reset"): llm.OutcomeNetwork,
	} {
		if got := outcome(err); got != want {
			t.Errorf("outcome(%v) = %s, want %s", err, got, want)
		}
	}
}
