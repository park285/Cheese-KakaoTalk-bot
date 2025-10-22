package irisfast

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/valyala/fasthttp"
)

// HeaderProvider allows injecting per-request headers
type HeaderProvider func() map[string]string

type Client struct {
	baseURL string
	http    *fasthttp.Client
	headers HeaderProvider

	defaultTimeout time.Duration
	retryMax       int
}

type Option func(*Client)

func WithTimeout(d time.Duration) Option {
	return func(c *Client) { c.defaultTimeout = d }
}

func WithMaxConnsPerHost(n int) Option {
	return func(c *Client) { c.http.MaxConnsPerHost = n }
}

func WithHeaderProvider(h HeaderProvider) Option {
	return func(c *Client) { c.headers = h }
}

func WithRetry(max int) Option {
	return func(c *Client) { c.retryMax = max }
}

func NewClient(baseURL string, opts ...Option) *Client {
	c := &Client{
		baseURL:        strings.TrimRight(baseURL, "/"),
		http:           &fasthttp.Client{ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, MaxConnsPerHost: 64},
		defaultTimeout: 10 * time.Second,
		retryMax:       3,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *Client) GetConfig(ctx context.Context) (*Config, error) {
	var cfg Config
	if err := c.doJSON(ctx, fasthttp.MethodGet, "/config", nil, &cfg, false); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Client) Decrypt(ctx context.Context, data string) (string, error) {
	req := DecryptRequest{Data: data}
	var resp DecryptResponse
	if err := c.doJSON(ctx, fasthttp.MethodPost, "/decrypt", req, &resp, true); err != nil {
		return "", err
	}
	return resp.Decrypted, nil
}

func (c *Client) SendMessage(ctx context.Context, room, message string) error {
	req := ReplyRequest{Type: "text", Room: room, Data: message}
	return c.doJSON(ctx, fasthttp.MethodPost, "/reply", req, nil, false)
}

func (c *Client) SendImage(ctx context.Context, room, imageBase64 string) error {
	req := ImageReplyRequest{Type: "image", Room: room, Data: imageBase64}
	return c.doJSON(ctx, fasthttp.MethodPost, "/reply", req, nil, false)
}

func (c *Client) doJSON(ctx context.Context, method, path string, in any, out any, retry bool) error {
	url := c.baseURL + path
	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer func() {
		fasthttp.ReleaseRequest(req)
		fasthttp.ReleaseResponse(resp)
	}()

	req.Header.SetMethod(method)
	req.SetRequestURI(url)
	req.Header.SetContentType("application/json")

	if c.headers != nil {
		for k, v := range c.headers() {
			if strings.TrimSpace(k) != "" && strings.TrimSpace(v) != "" {
				req.Header.Set(k, v)
			}
		}
	}

	if in != nil {
		payload, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		req.SetBody(payload)
	}

	attempts := 1
	if retry {
		attempts = c.retryMax
		if attempts <= 0 {
			attempts = 1
		}
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		deadline := c.computeDeadline(ctx)
		err := c.http.DoDeadline(req, resp, deadline)
		if err != nil {
			if attempt == attempts || !retry {
				return fmt.Errorf("request failed: %w", err)
			}
			lastErr = err
			if sleepErr := c.sleepWithContext(ctx, backoffDuration(attempt)); sleepErr != nil {
				return lastErr
			}
			continue
		}

		status := resp.StatusCode()
		if status < 200 || status >= 300 {
			body := string(resp.Body())
			err := fmt.Errorf("iris api error: status=%d body=%s", status, truncate(body, 512))
			if attempt == attempts || !retry || !shouldRetryStatus(status) {
				return err
			}
			lastErr = err
			if sleepErr := c.sleepWithContext(ctx, backoffDuration(attempt)); sleepErr != nil {
				return lastErr
			}
			continue
		}

		if out != nil {
			if err := json.Unmarshal(resp.Body(), out); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}
		}
		return nil
	}

	if lastErr == nil {
		lastErr = errors.New("unknown error")
	}
	return lastErr
}

func (c *Client) computeDeadline(ctx context.Context) time.Time {
	if dl, ok := ctx.Deadline(); ok {
		clientDL := time.Now().Add(c.defaultTimeout)
		if dl.Before(clientDL) {
			return dl
		}
		return clientDL
	}
	return time.Now().Add(c.defaultTimeout)
}

func (c *Client) sleepWithContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func backoffDuration(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 6 {
		attempt = 6
	}
	base := 100 * time.Millisecond
	return time.Duration(1<<uint(attempt-1)) * base // 100ms, 200ms ...
}

func shouldRetryStatus(code int) bool {
	switch code {
	case 500, 502, 503, 504:
		return true
	default:
		return false
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
