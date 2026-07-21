package uade

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
)

var ErrUnverifiedResponse = errors.New("uade response could not be verified")

// Client is the browserless UADE transport. Parsing/classification stays
// behind small methods so WebForms fixtures can be tested without the network.
type Client struct {
	HTTP    *http.Client
	BaseURL string
}

func NewClient(baseURL string) (*Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	return &Client{HTTP: &http.Client{Jar: jar, CheckRedirect: func(req *http.Request, _ []*http.Request) error {
		if req.URL.Scheme != "https" && req.URL.Scheme != "http" {
			return http.ErrUseLastResponse
		}
		return nil
	}}, BaseURL: strings.TrimRight(baseURL, "/")}, nil
}

func (c *Client) Fetch(ctx context.Context, path, username, password string) (string, error) {
	if c == nil || c.HTTP == nil {
		return "", errors.New("uade client is not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/"+strings.TrimLeft(path, "/"), nil)
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
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("%w: status %d", ErrTransient, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

var (
	ErrAuth      = errors.New("uade authentication failed")
	ErrTransient = errors.New("uade transient failure")
)
