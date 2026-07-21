package sso

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

var ErrMFARequired = errors.New("mfa or additional verification required")

type Result struct {
	StartURL string
	Manual   bool
}

func Relink(ctx context.Context, client *http.Client, loginURL, user, password string) (Result, error) {
	form := url.Values{"username": {user}, "password": {password}}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, loginURL, strings.NewReader(form.Encode()))
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
	return Result{StartURL: resp.Request.URL.String()}, nil
}
