// Package entra implements Microsoft Entra ID sign-in through the OAuth 2.0
// device authorization grant, so the server can call a Microsoft resource
// without a browser of its own and without ever holding a password.
//
// The user completes sign-in on any device of theirs; the server only polls.
// What persists is a single refresh token, encrypted at rest (see Store).
// The access token lives in memory only (see Session).
package entra

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OfficeClientID is Microsoft Office's public client. It is pre-authorized in
// virtually every tenant, which makes it the only option when registering an
// app is not possible -- but it is a grey area, and a tenant can block it with
// Conditional Access at any time. Prefer a client registered in Entra ID as a
// public client with delegated permissions, and override ClientID with it.
const OfficeClientID = "d3590ed6-52b3-4102-aeff-aad2292ab01c"

const defaultAuthority = "https://login.microsoftonline.com"

// Doer is the only network seam. Tests inject a scripted implementation, so
// nothing in this package's suite touches the network.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

type Config struct {
	TenantID string
	ClientID string
	// Resource is the root of the resource being accessed, with no path
	// (for example https://contoso.sharepoint.com or
	// https://graph.microsoft.com). The requested scope is derived from it.
	Resource string
	// Authority overrides login.microsoftonline.com. Only tests set it.
	Authority string
}

func (c Config) clientID() string {
	if c.ClientID != "" {
		return c.ClientID
	}
	return OfficeClientID
}

func (c Config) authority() string {
	if c.Authority != "" {
		return strings.TrimRight(c.Authority, "/")
	}
	return defaultAuthority
}

// Scope asks for every delegated permission already consented on the resource
// (.default) plus offline_access, without which Microsoft returns no refresh
// token and the session could never renew itself.
func (c Config) Scope() string {
	return strings.TrimRight(c.Resource, "/") + "/.default offline_access"
}

func (c Config) endpoint(name string) string {
	return c.authority() + "/" + url.PathEscape(c.TenantID) + "/oauth2/v2.0/" + name
}

func (c Config) Validate() error {
	switch {
	case c.TenantID == "":
		return errors.New("entra: tenant id is required")
	case c.Resource == "":
		return errors.New("entra: resource is required")
	}
	u, err := url.Parse(c.Resource)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return errors.New("entra: resource must be an https URL")
	}
	if strings.Trim(u.Path, "/") != "" {
		return errors.New("entra: resource must be the tenant root, without a path")
	}
	return nil
}

type Client struct {
	Config Config
	HTTP   Doer
	Now    func() time.Time
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Client) doer() Doer {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// DeviceCode is what the user needs to finish sign-in elsewhere. DeviceCode
// itself is a secret bearer of the pending grant and never leaves the server.
type DeviceCode struct {
	DeviceCode      string
	UserCode        string
	VerificationURI string
	ExpiresAt       time.Time
	Interval        time.Duration
}

// Tokens is a successful grant. AccessToken and RefreshToken are secrets:
// they never reach a log line, an error message, or the browser.
type Tokens struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
}

// tokenResponse covers both shapes the token endpoint answers with. The
// error_description field is deliberately not decoded: it carries Microsoft
// trace and correlation ids, and nothing derived from it may surface to the
// user or the logs.
type tokenResponse struct {
	AccessToken     string `json:"access_token"`
	RefreshToken    string `json:"refresh_token"`
	ExpiresIn       int64  `json:"expires_in"`
	Error           string `json:"error"`
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	Interval        int64  `json:"interval"`
}

// StartDeviceCode opens a device authorization grant.
func (c *Client) StartDeviceCode(ctx context.Context) (DeviceCode, error) {
	form := url.Values{"client_id": {c.Config.clientID()}, "scope": {c.Config.Scope()}}
	body, status, err := c.post(ctx, c.Config.endpoint("devicecode"), form)
	if err != nil {
		return DeviceCode{}, err
	}
	var r tokenResponse
	if json.Unmarshal(body, &r) != nil || status != http.StatusOK || r.DeviceCode == "" || r.UserCode == "" {
		return DeviceCode{}, classify(status, r.Error)
	}
	interval := time.Duration(r.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return DeviceCode{
		DeviceCode:      r.DeviceCode,
		UserCode:        r.UserCode,
		VerificationURI: r.VerificationURI,
		ExpiresAt:       c.now().Add(time.Duration(r.ExpiresIn) * time.Second),
		Interval:        interval,
	}, nil
}

// Sleeper waits d or returns early when ctx ends. Injectable so tests drive a
// whole polling session without waiting in real time.
type Sleeper func(ctx context.Context, d time.Duration) error

func realSleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// slowDownStep is the RFC 8628 increment: on slow_down the client must add
// five seconds to its polling interval for the rest of the grant.
const slowDownStep = 5 * time.Second

// PollDeviceCode waits for the user to finish sign-in. It returns the tokens,
// a *PermanentError when the grant can no longer succeed (expired, denied,
// invalid), or ctx's error if the caller gives up first.
func (c *Client) PollDeviceCode(ctx context.Context, dc DeviceCode, sleep Sleeper) (Tokens, error) {
	if sleep == nil {
		sleep = realSleep
	}
	interval := dc.Interval
	form := url.Values{
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		"client_id":   {c.Config.clientID()},
		"device_code": {dc.DeviceCode},
	}
	for {
		if !dc.ExpiresAt.IsZero() && !c.now().Before(dc.ExpiresAt) {
			return Tokens{}, &PermanentError{Code: "expired_token"}
		}
		if err := sleep(ctx, interval); err != nil {
			return Tokens{}, err
		}
		tokens, code, err := c.redeem(ctx, form)
		switch {
		case err == nil:
			return tokens, nil
		case code == "authorization_pending":
			continue
		case code == "slow_down":
			interval += slowDownStep
			continue
		}
		var transient *TransientError
		if errors.As(err, &transient) {
			// A blip mid-grant is not the user's fault and not a reason to
			// throw away a code they may be typing right now.
			continue
		}
		return Tokens{}, err
	}
}

// Refresh redeems a refresh token. Microsoft rotates refresh tokens: callers
// must store the returned one, and keep the previous one only when the
// response carries none (see Tokens.RefreshToken handling in Session).
func (c *Client) Refresh(ctx context.Context, refreshToken string) (Tokens, error) {
	tokens, _, err := c.redeem(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {c.Config.clientID()},
		"refresh_token": {refreshToken},
		"scope":         {c.Config.Scope()},
	})
	return tokens, err
}

// redeem posts to the token endpoint. The returned code is Microsoft's OAuth
// error code, exposed separately so the device-code loop can branch on the two
// non-terminal ones without them becoming errors anyone has to unwrap.
func (c *Client) redeem(ctx context.Context, form url.Values) (Tokens, string, error) {
	body, status, err := c.post(ctx, c.Config.endpoint("token"), form)
	if err != nil {
		return Tokens{}, "", err
	}
	var r tokenResponse
	if json.Unmarshal(body, &r) != nil {
		if status >= 500 {
			return Tokens{}, "", &TransientError{Status: status}
		}
		return Tokens{}, "", &PermanentError{Code: "malformed_response"}
	}
	if status == http.StatusOK && r.AccessToken != "" {
		return Tokens{
			AccessToken:  r.AccessToken,
			RefreshToken: r.RefreshToken,
			ExpiresAt:    c.now().Add(time.Duration(r.ExpiresIn) * time.Second),
		}, "", nil
	}
	return Tokens{}, r.Error, classify(status, r.Error)
}

func (c *Client) post(ctx context.Context, endpoint string, form url.Values) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := c.doer().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		return nil, 0, &TransientError{}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, resp.StatusCode, &TransientError{Status: resp.StatusCode}
	}
	return body, resp.StatusCode, nil
}

// permanentCodes are the OAuth errors after which retrying the same grant or
// refresh token cannot succeed. Anything here kills the session.
var permanentCodes = map[string]bool{
	"invalid_grant":          true,
	"expired_token":          true,
	"access_denied":          true,
	"authorization_declined": true,
	"bad_verification_code":  true,
	"invalid_client":         true,
	"unauthorized_client":    true,
	"invalid_scope":          true,
}

func classify(status int, code string) error {
	if permanentCodes[code] {
		return &PermanentError{Code: code}
	}
	if code == "authorization_pending" || code == "slow_down" {
		return &PendingError{Code: code}
	}
	if status >= 500 || status == http.StatusTooManyRequests || status == 0 {
		return &TransientError{Status: status}
	}
	return &PermanentError{Code: "unexpected_response"}
}

// PermanentError means the grant or refresh token is dead. Code is the OAuth
// error code only -- never Microsoft's description.
type PermanentError struct{ Code string }

func (e *PermanentError) Error() string { return "entra: permanent failure (" + e.Code + ")" }

// TransientError is a network failure, 5xx or throttle: the session is still
// valid and the next attempt may succeed.
type TransientError struct{ Status int }

func (e *TransientError) Error() string {
	if e.Status == 0 {
		return "entra: transient network failure"
	}
	return fmt.Sprintf("entra: transient failure (status %d)", e.Status)
}

// PendingError reports authorization_pending/slow_down outside the polling
// loop, which handles both itself.
type PendingError struct{ Code string }

func (e *PendingError) Error() string { return "entra: authorization " + e.Code }

func IsPermanent(err error) bool {
	var p *PermanentError
	return errors.As(err, &p)
}
