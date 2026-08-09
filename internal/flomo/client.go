package flomo

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
	"github.com/songquanpeng/flomo-cli/internal/domain"
)

type APIError struct {
	Code    int
	Message string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("flomo API error (code %d)", e.Code)
	}
	return fmt.Sprintf("flomo API: %s (code %d)", e.Message, e.Code)
}

type Client struct {
	baseURL     string
	httpClient  *http.Client
	accessToken string
	deviceID    string
	now         func() time.Time
	sleep       func(context.Context, time.Duration) error
	memoSyncMu  sync.Mutex
	lastMemoEnd time.Time
}

type Option func(*Client)

func WithHTTPClient(client *http.Client) Option {
	return func(c *Client) { c.httpClient = client }
}

func WithClock(now func() time.Time) Option {
	return func(c *Client) { c.now = now }
}

func NewClient(baseURL, accessToken, deviceID string, options ...Option) *Client {
	client := &Client{
		baseURL:     strings.TrimRight(baseURL, "/"),
		accessToken: accessToken,
		deviceID:    deviceID,
		httpClient:  &http.Client{Timeout: 60 * time.Second},
		now:         time.Now,
		sleep:       sleepContext,
	}
	for _, option := range options {
		option(client)
	}
	return client
}

func (c *Client) Login(ctx context.Context, account, password string) (domain.LoginResult, error) {
	params := map[string]any{"email": strings.TrimSpace(account), "password": strings.TrimSpace(password)}
	var result loginDTO
	if err := c.call(ctx, http.MethodPost, 1, "/user/login_by_email", params, &result); err != nil {
		return domain.LoginResult{}, err
	}
	if result.AccessToken == "" {
		return domain.LoginResult{}, errors.New("flomo login succeeded without an access token")
	}
	return result.domain(), nil
}

func (c *Client) Me(ctx context.Context) (domain.User, error) {
	var me userDTO
	if err := c.call(ctx, http.MethodGet, 1, "/user/me", nil, &me); err != nil {
		return domain.User{}, err
	}
	return me.domain(), nil
}

func (c *Client) UpdatedMemos(ctx context.Context, limit int, latestUpdatedAt int64, latestSlug, timezone string) ([]domain.Memo, error) {
	// flomo web serializes this cursor endpoint and waits 500ms after the
	// previous v1 sync call completes. Holding the lock also protects callers
	// that accidentally attempt concurrent syncs through one client.
	c.memoSyncMu.Lock()
	defer c.memoSyncMu.Unlock()
	if !c.lastMemoEnd.IsZero() {
		if wait := memoSyncInterval - c.now().Sub(c.lastMemoEnd); wait > 0 {
			if err := c.sleep(ctx, wait); err != nil {
				return nil, err
			}
		}
	}
	defer func() { c.lastMemoEnd = c.now() }()

	params := map[string]any{
		"limit":             limit,
		"latest_updated_at": latestUpdatedAt,
		"latest_slug":       latestSlug,
		"tz":                timezone,
	}
	var memos []memoDTO
	if err := c.call(ctx, http.MethodGet, 1, "/memo/updated/", params, &memos); err != nil {
		return nil, err
	}
	result := make([]domain.Memo, 0, len(memos))
	for _, memo := range memos {
		result = append(result, memo.domain())
	}
	return result, nil
}

func (c *Client) AddMemo(ctx context.Context, options domain.AddMemoRequest) (domain.Memo, error) {
	params := map[string]any{
		"content":    options.Content,
		"created_at": options.CreatedAt,
		"source":     options.Source,
		"memo_from":  options.MemoFrom,
		"file_ids":   []any{},
		"tz":         options.Timezone,
	}
	var memo memoDTO
	if err := c.call(ctx, http.MethodPost, 2, "/memo", params, &memo); err != nil {
		return domain.Memo{}, err
	}
	return memo.domain(), nil
}

func (c *Client) call(ctx context.Context, method string, version int, path string, input map[string]any, output any) error {
	params := make(map[string]any, len(input)+6)
	for key, value := range input {
		params[key] = value
	}
	params["timestamp"] = c.now().Unix()
	params["api_key"] = "flomo_web"
	params["app_version"] = "4.0"
	params["platform"] = "web"
	params["sign"] = sign(params)

	requestURL := fmt.Sprintf("%s/api/v%d%s", c.baseURL, version, path)
	var body io.Reader
	if method == http.MethodGet || method == http.MethodDelete {
		parsed, err := url.Parse(requestURL)
		if err != nil {
			return fmt.Errorf("build request URL: %w", err)
		}
		query := parsed.Query()
		for key, value := range params {
			addQuery(query, key, value)
		}
		parsed.RawQuery = query.Encode()
		requestURL = parsed.String()
	} else {
		payload, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		body = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, requestURL, body)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	c.applyWebHeaders(req, body != nil)
	if c.accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.accessToken)
	}

	var responseBody []byte
	var statusCode int
	for attempt := 0; ; attempt++ {
		resp, err := c.httpClient.Do(req.Clone(ctx))
		if err != nil {
			return fmt.Errorf("request flomo: %w", err)
		}
		statusCode = resp.StatusCode
		responseBody, err = readResponseBody(resp)
		if err != nil {
			return fmt.Errorf("read flomo response: %w", err)
		}
		if method == http.MethodGet && statusCode == http.StatusForbidden && isGatewayForbidden(responseBody) && attempt < len(gatewayRetryBackoff) {
			if err := c.sleep(ctx, gatewayRetryBackoff[attempt]); err != nil {
				return err
			}
			continue
		}
		break
	}
	if statusCode < 200 || statusCode >= 300 {
		return fmt.Errorf("flomo HTTP %d: %s", statusCode, responseSnippet(responseBody))
	}
	var envelope struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(responseBody, &envelope); err != nil {
		return fmt.Errorf("decode flomo response: %w", err)
	}
	if envelope.Code != 0 {
		return &APIError{Code: envelope.Code, Message: envelope.Message}
	}
	if output != nil && len(envelope.Data) > 0 && string(envelope.Data) != "null" {
		if err := json.Unmarshal(envelope.Data, output); err != nil {
			return fmt.Errorf("decode flomo data: %w", err)
		}
	}
	return nil
}

func readResponseBody(response *http.Response) ([]byte, error) {
	defer response.Body.Close()
	var reader io.Reader = response.Body
	var closeDecoder func()
	switch strings.ToLower(strings.TrimSpace(response.Header.Get("Content-Encoding"))) {
	case "", "identity":
	case "gzip":
		decoder, err := gzip.NewReader(response.Body)
		if err != nil {
			return nil, err
		}
		reader = decoder
		closeDecoder = func() { _ = decoder.Close() }
	case "deflate":
		decoder, err := zlib.NewReader(response.Body)
		if err != nil {
			return nil, err
		}
		reader = decoder
		closeDecoder = func() { _ = decoder.Close() }
	case "br":
		reader = brotli.NewReader(response.Body)
	case "zstd":
		decoder, err := zstd.NewReader(response.Body)
		if err != nil {
			return nil, err
		}
		reader = decoder
		closeDecoder = decoder.Close
	default:
		return nil, fmt.Errorf("unsupported content encoding %q", response.Header.Get("Content-Encoding"))
	}
	if closeDecoder != nil {
		defer closeDecoder()
	}
	return io.ReadAll(io.LimitReader(reader, 8<<20))
}

func isGatewayForbidden(body []byte) bool {
	value := strings.ToLower(string(body))
	return strings.Contains(value, "<html") && strings.Contains(value, "403 forbidden") && strings.Contains(value, "nginx")
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func addQuery(query url.Values, key string, value any) {
	switch values := value.(type) {
	case []string:
		for _, item := range values {
			query.Add(key+"[]", item)
		}
	case []any:
		for _, item := range values {
			query.Add(key+"[]", fmt.Sprint(item))
		}
	default:
		query.Set(key, fmt.Sprint(value))
	}
}

func responseSnippet(body []byte) string {
	const max = 300
	value := strings.TrimSpace(string(body))
	if len(value) > max {
		value = value[:max] + "..."
	}
	return strconv.QuoteToASCII(value)
}
