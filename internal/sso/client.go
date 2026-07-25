package sso

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

// maxSSOBodyBytes bounds every response body this package reads, regardless
// of which host in the allowlist served it.
const maxSSOBodyBytes = 500_000

// allowedHosts returns the lowercase set of hosts the SSO flow is permitted
// to talk to: the configured portal host plus the current value of
// microsoftLoginHost (a package-level var, not a const, so tests can point
// it at an httptest server without touching any other logic).
func allowedHosts(portalURL string) (map[string]bool, error) {
	u, err := url.Parse(portalURL)
	if err != nil || u.Host == "" {
		return nil, errors.New("invalid sso portal URL")
	}
	return map[string]bool{
		strings.ToLower(u.Hostname()):       true,
		strings.ToLower(microsoftLoginHost): true,
	}, nil
}

// NewClient builds an http.Client scoped to the SSO flow: cookie jar for the
// multi-hop portal/Microsoft dance, a bounded timeout, and a CheckRedirect
// that fails closed on any host outside allowedHosts, on an https->http
// downgrade, or past 8 chained redirects.
func NewClient(portalURL string) (*http.Client, error) {
	allowed, err := allowedHosts(portalURL)
	if err != nil {
		return nil, err
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Jar:     jar,
		Timeout: 20 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if !allowed[strings.ToLower(req.URL.Hostname())] {
				return errors.New("sso redirect outside allowed hosts")
			}
			if len(via) > 0 && via[len(via)-1].URL.Scheme == "https" && req.URL.Scheme != "https" {
				return errors.New("sso redirect downgraded to insecure scheme")
			}
			if len(via) >= 8 {
				return errors.New("too many sso redirects")
			}
			return nil
		},
	}, nil
}

// boundedFetcher is the only way internal/sso issues explicit (non-redirect)
// requests. Its get/post methods re-validate the target host before every
// request -- defense in depth independent of http.Client's CheckRedirect,
// because these targets come from parsed HTML (hrefs/form actions), not just
// server-issued redirects.
type boundedFetcher struct {
	http    *http.Client
	allowed map[string]bool
}

func newBoundedFetcher(client *http.Client, portalURL string) (boundedFetcher, error) {
	if client == nil {
		return boundedFetcher{}, errors.New("sso http client is required")
	}
	allowed, err := allowedHosts(portalURL)
	if err != nil {
		return boundedFetcher{}, err
	}
	return boundedFetcher{http: client, allowed: allowed}, nil
}

func (f boundedFetcher) get(ctx context.Context, target string) (finalURL, body string, err error) {
	if err := f.checkAllowed(target); err != nil {
		return "", "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", "", err
	}
	return f.do(req)
}

func (f boundedFetcher) post(ctx context.Context, target string, values url.Values) (finalURL, body string, err error) {
	if err := f.checkAllowed(target); err != nil {
		return "", "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(values.Encode()))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return f.do(req)
}

func (f boundedFetcher) checkAllowed(target string) error {
	u, err := url.Parse(target)
	if err != nil || u.Host == "" {
		return errors.New("invalid sso request target")
	}
	if !f.allowed[strings.ToLower(u.Hostname())] {
		return errors.New("sso request target host not allowed")
	}
	return nil
}

func (f boundedFetcher) do(req *http.Request) (string, string, error) {
	resp, err := f.http.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("sso request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("sso request failed: status %d", resp.StatusCode)
	}
	b, err := readBounded(resp.Body, maxSSOBodyBytes)
	if err != nil {
		return "", "", err
	}
	return resp.Request.URL.String(), string(b), nil
}

// readBounded mirrors internal/uade/client.go's helper. Duplicated locally
// on purpose -- internal/sso and internal/uade stay decoupled, no cross
// import for a five-line helper.
func readBounded(reader io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		return nil, errors.New("invalid sso body limit")
	}
	b, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, errors.New("sso response too large")
	}
	return b, nil
}

// resolveURL resolves ref against base and rejects any resulting scheme
// other than http/https -- guards against following a "javascript:"/
// "mailto:" href extracted from parsed HTML.
func resolveURL(base, ref string) (string, error) {
	baseURL, err := url.Parse(base)
	if err != nil {
		return "", errors.New("invalid sso base URL")
	}
	refURL, err := url.Parse(ref)
	if err != nil {
		return "", errors.New("invalid sso reference URL")
	}
	resolved := baseURL.ResolveReference(refURL)
	if resolved.Scheme != "http" && resolved.Scheme != "https" {
		return "", errors.New("sso reference URL scheme not allowed")
	}
	return resolved.String(), nil
}
