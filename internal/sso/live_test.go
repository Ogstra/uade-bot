package sso

// TestRelinkAgainstRealUADESite is the harness that executes the human
// checkpoint of 03.3-17-PLAN.md against the real UADE/Microsoft sites. It
// must NEVER run in CI -- it is skipped unless UADE_LIVE_SSO_TEST=true is
// set explicitly, and even then it talks to real, non-test accounts.
//
// If step 2 (the login trigger discovery in followLoginTrigger /
// findLoginTrigger / resolveLoginTrigger) fails against the real site, the
// human running this must capture the real /Account/Login HTML (e.g. a
// temporary bounded t.Logf, or inspecting with curl/devtools -- never log
// the full page unbounded, and never log credentials) and correct
// findLoginTrigger/resolveLoginTrigger in internal/sso/relink.go before
// treating the checkpoint as approved. Step 2 is the one piece of the
// 8-step flow explore-sso-flow.js only confirmed via Playwright role
// locators, not raw HTTP.

import (
	"context"
	"errors"
	"net/url"
	"os"
	"testing"
	"time"
)

func TestRelinkAgainstRealUADESite(t *testing.T) {
	if os.Getenv("UADE_LIVE_SSO_TEST") != "true" {
		t.Skip("set UADE_LIVE_SSO_TEST=true, UADE_USERNAME and UADE_PASSWORD " +
			"(optionally UADE_SSO_PORTAL_URL, default https://inscripciones.uade.edu.ar/) " +
			"to run this live checkpoint for 03.3-17-PLAN.md; skipped by default and never run in CI")
	}

	username := os.Getenv("UADE_USERNAME")
	password := os.Getenv("UADE_PASSWORD")
	if username == "" || password == "" {
		t.Fatal("UADE_LIVE_SSO_TEST=true requires UADE_USERNAME and UADE_PASSWORD to also be set")
	}
	portalURL := os.Getenv("UADE_SSO_PORTAL_URL")
	if portalURL == "" {
		portalURL = "https://inscripciones.uade.edu.ar/"
	}
	if _, err := url.Parse(portalURL); err != nil {
		t.Fatalf("invalid UADE_SSO_PORTAL_URL: %v", err)
	}

	client, err := NewClient(portalURL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	result, err := Relink(ctx, client, portalURL, username, password)
	switch {
	case err == nil:
		// Never log the URL itself -- it carries paramAlumId and an
		// internal cookie= value, equivalent to a session credential.
		t.Logf("relink ok, start url length=%d", len(result.StartURL))
		if !ValidStartURL(result.StartURL) {
			t.Fatal("Relink returned a StartURL that ValidStartURL rejects")
		}
	case errors.Is(err, ErrMFARequired):
		// A valid outcome, not a test failure -- the human running this
		// must confirm manually whether the account used actually has
		// MFA/additional verification enabled.
		t.Logf("relink stopped at MFA/additional verification, as expected for an MFA account")
	default:
		// internal/sso's errors are fixed sentinels/messages, never
		// interpolated with secrets or full HTML, so it's safe to include
		// err in the failure message.
		t.Fatalf("Relink failed: %v", err)
	}
}
