package uade

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

var ErrUnverifiedResponse = errors.New("uade response could not be verified")

// Client is the browserless UADE transport. Parsing/classification stays
// behind small methods so WebForms fixtures can be tested without the network.
type Client struct {
	HTTP          *http.Client
	BaseURL       string
	AllowedOrigin string
	MaxBodyBytes  int64
}

func NewClient(baseURL string) (*Client, error) {
	base, err := url.Parse(baseURL)
	if err != nil || base.Scheme == "" || base.Host == "" || base.User != nil {
		return nil, errors.New("invalid uade base URL")
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	origin := base.Scheme + "://" + base.Host
	return &Client{HTTP: &http.Client{Jar: jar, Timeout: 10 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme+"://"+req.URL.Host != origin || (len(via) > 0 && via[len(via)-1].URL.Scheme == "https" && req.URL.Scheme != "https") {
			return errors.New("uade redirect outside allowed origin")
		}
		if len(via) >= 5 {
			return errors.New("too many uade redirects")
		}
		return nil
	}}, BaseURL: strings.TrimRight(baseURL, "/"), AllowedOrigin: origin, MaxBodyBytes: 300_000}, nil
}

func (c *Client) Fetch(ctx context.Context, path, username, password string) (string, error) {
	if c == nil || c.HTTP == nil {
		return "", errors.New("uade client is not configured")
	}
	target, err := c.resolve(path)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(username, password)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("uade request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return "", fmt.Errorf("%w: status %d", ErrAuth, resp.StatusCode)
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return "", fmt.Errorf("%w: status %d", ErrRateLimit, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("%w: status %d", ErrTransient, resp.StatusCode)
	}
	b, err := readBounded(resp.Body, c.MaxBodyBytes)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (c *Client) resolve(path string) (string, error) {
	base, err := url.Parse(c.BaseURL + "/")
	if err != nil {
		return "", ErrTransient
	}
	reference, err := url.Parse(path)
	if err != nil {
		return "", ErrTransient
	}
	target := base.ResolveReference(reference)
	if target.Scheme+"://"+target.Host != c.AllowedOrigin {
		return "", errors.New("uade URL outside allowed origin")
	}
	return target.String(), nil
}

func readBounded(reader io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		return nil, errors.New("invalid uade body limit")
	}
	b, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%w: response too large", ErrTransient)
	}
	return b, nil
}

var (
	ErrAuth      = errors.New("uade authentication failed")
	ErrTransient = errors.New("uade transient failure")
	ErrRateLimit = fmt.Errorf("%w: rate limited", ErrTransient)
)
