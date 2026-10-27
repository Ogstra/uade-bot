package entra

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
)

// ResourceValidator checks a token with a real GET against probeURL. Any 2xx
// passes; everything else -- 401, 403 from Conditional Access, a redirect to
// a login page -- fails, because each means the resource will not serve this
// token.
func ResourceValidator(doer Doer, probeURL string) Validator {
	return func(ctx context.Context, accessToken string) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)
		req.Header.Set("Accept", "application/json")
		resp, err := doer.Do(req)
		if err != nil {
			return &TransientError{}
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}
		return errors.New("entra: resource rejected the token")
	}
}

// DefaultProbeURL picks a cheap authenticated endpoint for resource: the
// signed-in user for Microsoft Graph, the root web for SharePoint. Override
// with ENTRA_VALIDATE_URL for anything else.
func DefaultProbeURL(resource string) string {
	root := strings.TrimRight(resource, "/")
	if strings.EqualFold(root, "https://graph.microsoft.com") {
		return root + "/v1.0/me"
	}
	return root + "/_api/web"
}
