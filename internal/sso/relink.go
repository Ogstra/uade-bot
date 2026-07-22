package sso

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

var ErrMFARequired = errors.New("mfa or additional verification required")
var ErrInvalidStartURL = errors.New("sso did not produce a valid enrollment URL")

const microsoftLoginHost = "login.microsoftonline.com"
const enrollmentStartHost = "inscripcionespia.uade.edu.ar"

type Result struct {
	StartURL string
	Manual   bool
}

type Fallback func(context.Context, string, string) (Result, error)

func MicrosoftEmail(username string) string {
	if strings.Contains(username, "@") {
		return username
	}
	return username + "@uade.edu.ar"
}

func IsMicrosoftLogin(rawURL string) bool {
	u, err := url.Parse(rawURL)
	return err == nil && strings.EqualFold(u.Hostname(), microsoftLoginHost)
}

func ValidStartURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	return err == nil && u.Scheme == "https" && strings.EqualFold(u.Hostname(), enrollmentStartHost) && u.Query().Has("param")
}

func Relink(ctx context.Context, client *http.Client, loginURL, user, password string) (Result, error) {
	if client == nil {
		return Result{}, errors.New("sso client is required")
	}
	login, err := url.Parse(loginURL)
	if err != nil || login.Host == "" || (login.Scheme != "https" && login.Hostname() != "127.0.0.1" && login.Hostname() != "localhost") {
		return Result{}, errors.New("invalid sso login URL")
	}
	form := url.Values{"username": {user}, "password": {password}}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, login.String(), strings.NewReader(form.Encode()))
	if e != nil {
		return Result{}, e
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, e := client.Do(req)
	if e != nil {
		return Result{}, e
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return Result{Manual: true}, ErrMFARequired
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Result{}, errors.New("sso request failed")
	}
	finalURL := resp.Request.URL.String()
	if IsMicrosoftLogin(finalURL) {
		return Result{Manual: true}, ErrMFARequired
	}
	if !ValidStartURL(finalURL) {
		return Result{}, ErrInvalidStartURL
	}
	return Result{StartURL: finalURL}, nil
}

// RelinkWithFallback keeps browser/manual work lazy: the fallback is invoked
// only for an explicit MFA/additional-verification outcome, never for a
// transport error or a malformed final URL.
func RelinkWithFallback(ctx context.Context, client *http.Client, loginURL, user, password string, fallback Fallback) (Result, error) {
	result, err := Relink(ctx, client, loginURL, user, password)
	if !errors.Is(err, ErrMFARequired) || fallback == nil {
		return result, err
	}
	return fallback(ctx, user, password)
}
