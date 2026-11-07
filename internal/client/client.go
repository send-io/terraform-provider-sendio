// Package client is a small send.io REST client used by the Terraform
// provider. It speaks JSON over net/http, retries rate-limited and transient
// failures, and turns error bodies ({"statusCode","name","message"}) into
// *APIError values.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is the production API.
const DefaultBaseURL = "https://api.send.io"

// Client calls the send.io API with one API key.
type Client struct {
	BaseURL   string
	APIKey    string
	UserAgent string
	HTTP      *http.Client
	// MaxRetries is how many times a 429 or 502/503/504 is retried.
	MaxRetries int
	// RetryWait is the first backoff step (doubled per attempt) when the
	// response has no Retry-After header.
	RetryWait time.Duration
}

// New returns a client for baseURL (DefaultBaseURL when empty).
func New(baseURL, apiKey, userAgent string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		APIKey:     apiKey,
		UserAgent:  userAgent,
		HTTP:       &http.Client{Timeout: 60 * time.Second},
		MaxRetries: 4,
		RetryWait:  time.Second,
	}
}

// APIError is an error response from the API.
type APIError struct {
	StatusCode int    `json:"statusCode"`
	Name       string `json:"name"`
	Message    string `json:"message"`
	Method     string `json:"-"`
	Path       string `json:"-"`
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.StatusCode)
	}
	if e.Name != "" {
		return fmt.Sprintf("%s %s: %d %s: %s", e.Method, e.Path, e.StatusCode, e.Name, msg)
	}
	return fmt.Sprintf("%s %s: %d: %s", e.Method, e.Path, e.StatusCode, msg)
}

// IsNotFound reports whether err is a 404 from the API.
func IsNotFound(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.StatusCode == http.StatusNotFound
}

// StatusCode returns the HTTP status of an API error, or 0.
func StatusCode(err error) int {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.StatusCode
	}
	return 0
}

// Do sends method path with body (JSON-encoded when not nil) and decodes a
// 2xx response into out (when not nil).
func (c *Client) Do(ctx context.Context, method, path string, body, out any) error {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
	}
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if c.UserAgent != "" {
			req.Header.Set("User-Agent", c.UserAgent)
		}
		res, err := c.HTTP.Do(req)
		if err != nil {
			// A connection error on a read is safe to try again.
			if method == http.MethodGet && attempt < c.MaxRetries && ctx.Err() == nil {
				if werr := c.wait(ctx, attempt, ""); werr != nil {
					return werr
				}
				continue
			}
			return fmt.Errorf("%s %s: %w", method, path, err)
		}
		raw, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
		res.Body.Close()
		if err != nil {
			return fmt.Errorf("%s %s: read response: %w", method, path, err)
		}
		if res.StatusCode >= 200 && res.StatusCode < 300 {
			if out == nil || len(bytes.TrimSpace(raw)) == 0 {
				return nil
			}
			if err := json.Unmarshal(raw, out); err != nil {
				return fmt.Errorf("%s %s: decode response: %w", method, path, err)
			}
			return nil
		}
		if c.retryable(method, res.StatusCode) && attempt < c.MaxRetries {
			if werr := c.wait(ctx, attempt, res.Header.Get("Retry-After")); werr != nil {
				return werr
			}
			continue
		}
		ae := &APIError{StatusCode: res.StatusCode, Method: method, Path: path}
		if json.Unmarshal(raw, ae) != nil || ae.Message == "" {
			ae.Message = strings.TrimSpace(string(raw))
			if len(ae.Message) > 500 {
				ae.Message = ae.Message[:500]
			}
		}
		ae.StatusCode = res.StatusCode
		return ae
	}
}

// retryable: a 429 was not processed, so any method may be sent again; a
// gateway error is only retried for reads and deletes.
func (c *Client) retryable(method string, status int) bool {
	if status == http.StatusTooManyRequests {
		return true
	}
	if status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout {
		return method == http.MethodGet || method == http.MethodDelete
	}
	return false
}

func (c *Client) wait(ctx context.Context, attempt int, retryAfter string) error {
	d := c.RetryWait << attempt
	if s, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil && s >= 0 {
		d = time.Duration(s) * time.Second
	}
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Get, Post, Patch and Delete are shorthands for Do.
func (c *Client) Get(ctx context.Context, path string, out any) error {
	return c.Do(ctx, http.MethodGet, path, nil, out)
}
func (c *Client) Post(ctx context.Context, path string, body, out any) error {
	if body == nil {
		body = map[string]any{}
	}
	return c.Do(ctx, http.MethodPost, path, body, out)
}
func (c *Client) Patch(ctx context.Context, path string, body, out any) error {
	return c.Do(ctx, http.MethodPatch, path, body, out)
}
func (c *Client) Delete(ctx context.Context, path string) error {
	return c.Do(ctx, http.MethodDelete, path, nil, nil)
}

// Path joins escaped segments onto a base path: Path("/domains", id).
func Path(base string, segments ...string) string {
	var b strings.Builder
	b.WriteString(base)
	for _, s := range segments {
		b.WriteByte('/')
		b.WriteString(url.PathEscape(s))
	}
	return b.String()
}

type listPage[T any] struct {
	Data    []T  `json:"data"`
	HasMore bool `json:"has_more"`
}

// ListAll reads every page of a list endpoint (limit=100, after=<last id>).
// id returns an item's id for the cursor.
func ListAll[T any](ctx context.Context, c *Client, path string, id func(T) string) ([]T, error) {
	var all []T
	after := ""
	for page := 0; page < 1000; page++ {
		q := url.Values{"limit": {"100"}}
		if after != "" {
			q.Set("after", after)
		}
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		var p listPage[T]
		if err := c.Get(ctx, path+sep+q.Encode(), &p); err != nil {
			return nil, err
		}
		all = append(all, p.Data...)
		if !p.HasMore || len(p.Data) == 0 {
			return all, nil
		}
		after = id(p.Data[len(p.Data)-1])
	}
	return all, nil
}
