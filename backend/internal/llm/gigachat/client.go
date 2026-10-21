// Package gigachat implements llm.Client for the GigaChat API (Sber).
//
// Specifics handled here:
//   - OAuth: an authorization key is exchanged for an access token that lives
//     30 minutes; the token is cached and refreshed ahead of expiry or on 401.
//   - TLS: GigaChat endpoints use certificates of the Russian Ministry of Digital
//     Development, so its root CA is embedded and trusted in addition to the system pool.
//   - Function calling uses GigaChat's own format (functions / function_call /
//     role "function" / functions_state_id), not OpenAI's tools.
package gigachat

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/rics69/ai-expense-tracking-bot/backend/internal/llm"
)

//go:embed russian_trusted_root_ca.pem
var russianRootCA []byte

const (
	DefaultBaseURL = "https://api.giga.chat/v1"
	DefaultAuthURL = "https://ngw.devices.sberbank.ru:9443/api/v2/oauth"
	DefaultModel   = "GigaChat-2-Pro"
	DefaultScope   = "GIGACHAT_API_PERS"

	// Refresh the token this long before it expires.
	tokenRefreshMargin = time.Minute
)

// Config configures the client. Only AuthKey is required.
type Config struct {
	AuthKey     string // base64(client_id:client_secret) from the GigaChat console
	Scope       string
	Model       string
	BaseURL     string
	AuthURL     string
	Temperature *float64
	Timeout     time.Duration // per HTTP request
	MaxRetries  int           // on 429 and 5xx
	// MaxConcurrent limits parallel requests: the free tier allows very few.
	MaxConcurrent int
	// InsecureSkipVerify disables TLS verification. Local debugging only.
	InsecureSkipVerify bool
}

// Client talks to the GigaChat REST API.
type Client struct {
	cfg  Config
	http *http.Client
	log  *slog.Logger
	sem  chan struct{}
	now  func() time.Time

	// sleep waits between retries; replaced in tests.
	sleep func(ctx context.Context, d time.Duration) error

	mu          sync.Mutex
	token       string
	tokenExpiry time.Time
}

var _ llm.Client = (*Client)(nil)

// New creates a client, filling unset config fields with defaults.
func New(cfg Config, log *slog.Logger) (*Client, error) {
	if cfg.AuthKey == "" {
		return nil, errors.New("gigachat: auth key is required")
	}
	if cfg.Scope == "" {
		cfg.Scope = DefaultScope
	}
	if cfg.Model == "" {
		cfg.Model = DefaultModel
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.AuthURL == "" {
		cfg.AuthURL = DefaultAuthURL
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = 1
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")

	tlsCfg, err := tlsConfig(cfg.InsecureSkipVerify)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = tlsCfg

	return &Client{
		cfg:   cfg,
		http:  &http.Client{Transport: transport, Timeout: cfg.Timeout},
		log:   log,
		sem:   make(chan struct{}, cfg.MaxConcurrent),
		now:   time.Now,
		sleep: sleepCtx,
	}, nil
}

func tlsConfig(insecure bool) (*tls.Config, error) {
	if insecure {
		return &tls.Config{InsecureSkipVerify: true}, nil //nolint:gosec // explicit opt-in for local debugging
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(russianRootCA) {
		return nil, errors.New("gigachat: cannot parse embedded root CA")
	}
	return &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, nil
}

// Chat sends one chat completion request.
func (c *Client) Chat(ctx context.Context, req llm.Request) (llm.Response, error) {
	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-ctx.Done():
		return llm.Response{}, ctx.Err()
	}

	body, err := json.Marshal(c.buildRequest(req))
	if err != nil {
		return llm.Response{}, fmt.Errorf("gigachat: encode request: %w", err)
	}

	var resp chatResponse
	err = c.doWithRetry(ctx, body, req.SessionID, &resp)
	var out llm.Response
	if err == nil {
		out, err = convertResponse(resp)
	}
	if err != nil {
		return llm.Response{}, err
	}
	c.log.DebugContext(ctx, "gigachat call",
		"model", resp.Model,
		"prompt_tokens", out.Usage.PromptTokens,
		"cached_tokens", out.Usage.CachedTokens,
		"completion_tokens", out.Usage.CompletionTokens,
		"tool_call", out.Message.ToolCall != nil)
	return out, nil
}

// doWithRetry posts to /chat/completions, refreshing the token once on 401
// and retrying network failures and 429/5xx with exponential backoff.
// Repeating a completion is safe: tools run on our side, not in the request.
func (c *Client) doWithRetry(ctx context.Context, body []byte, sessionID string, out *chatResponse) error {
	refreshed := false
	backoff := time.Second
	for attempt := 0; ; attempt++ {
		status, header, respBody, err := c.post(ctx, body, sessionID)
		if err != nil {
			if ctx.Err() != nil || attempt >= c.cfg.MaxRetries {
				return err
			}
			c.log.WarnContext(ctx, "gigachat retry", "err", err, "wait", backoff, "attempt", attempt+1)
			if err := c.sleep(ctx, backoff); err != nil {
				return err
			}
			backoff *= 2
			continue
		}
		switch {
		case status == http.StatusOK:
			if err := json.Unmarshal(respBody, out); err != nil {
				return fmt.Errorf("gigachat: decode response: %w", err)
			}
			return nil
		case status == http.StatusUnauthorized && !refreshed:
			refreshed = true
			c.invalidateToken()
			continue
		case (status == http.StatusTooManyRequests || status >= 500) && attempt < c.cfg.MaxRetries:
			wait := retryAfter(header, backoff)
			c.log.WarnContext(ctx, "gigachat retry", "status", status, "wait", wait, "attempt", attempt+1)
			if err := c.sleep(ctx, wait); err != nil {
				return err
			}
			backoff *= 2
			continue
		default:
			return &APIError{Status: status, Body: truncate(string(respBody), 500)}
		}
	}
}

func (c *Client) post(ctx context.Context, body []byte, sessionID string) (int, http.Header, []byte, error) {
	token, err := c.accessToken(ctx)
	if err != nil {
		return 0, nil, nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return 0, nil, nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token)
	if sessionID != "" {
		// Lets GigaChat reuse the cached prompt prefix of the same conversation.
		httpReq.Header.Set("X-Session-ID", sessionID)
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("gigachat: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return 0, nil, nil, fmt.Errorf("gigachat: read response: %w", err)
	}
	return resp.StatusCode, resp.Header, respBody, nil
}

// accessToken returns a cached token or fetches a new one.
// The mutex makes concurrent callers wait for a single refresh.
func (c *Client) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && c.now().Add(tokenRefreshMargin).Before(c.tokenExpiry) {
		return c.token, nil
	}

	form := url.Values{"scope": {c.cfg.Scope}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.AuthURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("RqUID", uuid.NewString())
	req.Header.Set("Authorization", "Basic "+c.cfg.AuthKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("gigachat: get token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gigachat: get token: %w", &APIError{Status: resp.StatusCode, Body: truncate(string(body), 300)})
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresAt   int64  `json:"expires_at"` // unix milliseconds
	}
	if err := json.Unmarshal(body, &tok); err != nil || tok.AccessToken == "" {
		return "", fmt.Errorf("gigachat: unexpected token response: %s", truncate(string(body), 300))
	}
	c.token = tok.AccessToken
	c.tokenExpiry = time.UnixMilli(tok.ExpiresAt)
	return c.token, nil
}

func (c *Client) invalidateToken() {
	c.mu.Lock()
	c.token = ""
	c.mu.Unlock()
}

// APIError is a non-successful HTTP response from GigaChat.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string { return fmt.Sprintf("gigachat: HTTP %d: %s", e.Status, e.Body) }

func retryAfter(h http.Header, fallback time.Duration) time.Duration {
	if s, err := strconv.Atoi(h.Get("Retry-After")); err == nil && s > 0 && s <= 60 {
		return time.Duration(s) * time.Second
	}
	return fallback
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
