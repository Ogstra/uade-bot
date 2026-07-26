package sso

// TestRelinkAgainstRealUADESite is the harness that executes the human
// checkpoint of 03.3-17-PLAN.md against the real UADE/Microsoft sites. It
// must NEVER run in CI -- it is skipped unless UADE_LIVE_SSO_TEST=true is
// set explicitly, and even then it talks to real, non-test accounts.
//
// Step 2 (the login trigger discovery in followLoginTrigger /
// findLoginTrigger / resolveLoginTrigger) was already confirmed correct
// against the real site during the first 03.3-17 attempt (see
// 03.3-17-live-verification-notes.md) -- it is not expected to fail again.
//
// If this run fails, the most likely point of failure is now the $Config
// step this plan (03.3-18) introduced: either parseMicrosoftConfig in
// msconfig.go (if Microsoft changed the $Config format/field names), or one
// of the speculative fixed POST defaults in submitMicrosoftLogin in
// relink.go (if one of those hardcoded hidden fields turns out to be
// required with a different value than assumed -- see the block comment
// there marking exactly which fields are speculative). The human running
// this must capture the real Microsoft response (e.g. a temporary bounded
// t.Logf, or inspecting with curl/devtools -- never log the full page
// unbounded, and never log credentials) and correct msconfig.go/relink.go
// accordingly before treating the checkpoint as approved.
//
// 03.3-19 added exactly that bounded capture: the real second attempt of
// this checkpoint returned ErrMFARequired ("relink stopped at MFA/
// additional verification") against a test account the user confirmed does
// NOT have MFA enabled -- a false positive, most likely caused by the
// speculative POST defaults in submitMicrosoftLogin (03.3-18) making
// Microsoft reject the session or show an interstitial indistinguishable
// from real MFA.
//
// 03.3-20 (fourth reopening of this checkpoint) generalized
// continueMicrosoftChain to auto-continue any Microsoft page with no <form>
// but a parseable $Config (continueViaMicrosoftConfig in relink.go),
// covering the hypothesis that the "relink stopped at MFA" false positive
// was actually the KMSI "Stay signed in?" interstitial (documented in
// explore-sso-flow.js ~217-234), which -- like the login page itself
// (03.3-18) -- is rendered client-side from its own $Config blob rather
// than a raw <form>. It also added MFADiagnostics.ConfigKeys (the NAMES,
// never the values, of the last $Config's keys) for the case that
// hypothesis still isn't enough. If the MFA branch below fires again,
// COPY AND PASTE BOTH new log lines: the second t.Logf's full line (AADSTS
// code, host, path, hops, from 03.3-19) AND the third t.Logf's line below it
// (MFA $Config keys, from 03.3-20, only printed when present) -- together
// they determine whether this plan's $Config auto-continuation hypothesis
// matched reality (in which case ConfigKeys should be empty, because Relink
// would already have left Microsoft) or whether the real page uses a
// still-different shape this plan didn't anticipate (in which case
// ConfigKeys carries the exact names to adjust
// buildMicrosoftContinuePostValues against, instead of another round of
// guessing).
//
// 03.3-20's fourth attempt confirmed the KMSI auto-continuation hypothesis
// worked: Microsoft authentication now completes end to end for the test
// account (no MFA, no more false-positive ErrMFARequired). The real failure
// moved to a DIFFERENT, later point: Relink now returns ErrInvalidStartURL
// ("sso did not produce a valid enrollment URL") once it's back on the
// final UADE portal page, because that page has no
// a.inscribite[data-tipolink="InscripcionAsignatura"] with a non-empty
// data-linkid. 03.3-21 (fifth reopening of this checkpoint) added exactly
// the bounded diagnostics needed to distinguish the two remaining
// hypotheses without ever seeing/logging the real data-linkid value:
//
//   - Hypothesis A ("no bug"): the test account genuinely has no
//     InscripcionAsignatura enrollment available right now.
//     InscribeteLinkCount == 0 (no a.inscribite element of ANY type) is the
//     strongest signal for this -- confirm the account's real enrollment
//     state before requesting another round of code changes.
//   - Hypothesis B ("selector bug"): DataTipolinkValues contains other
//     types (e.g. "CursosMRI") but never "InscripcionAsignatura" -- that IS
//     a concrete lead for a follow-up plan to adjust extractStartURL's
//     filter, informed by the real values observed here.
//
// If the ErrInvalidStartURL branch below fires, COPY AND PASTE its full
// t.Logf line (a.inscribite count, data-tipolink values, Bootstrap tab
// presence, host, path) before treating this checkpoint as failed.

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
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
		// 03.3-19: only these four non-sensitive fields ever cross into this
		// log line -- never password/email/sFT/sCtx/canary/sessionId/
		// hpgrequestid/the full HTML/the URL's query string.
		if diag, ok := MFADiagnosticsFrom(err); ok {
			code := diag.AADSTSCode
			if code == "" {
				code = "ninguno"
			}
			t.Logf("MFA diagnostics: aadsts=%s host=%s path=%s hops=%d", code, diag.Host, diag.Path, diag.Hops)
			// 03.3-20: only the KEY NAMES of the last $Config seen (never
			// any value behind them) -- printed only when present, to avoid
			// noise in the common case where the final page had no $Config
			// at all.
			if len(diag.ConfigKeys) > 0 {
				t.Logf("MFA $Config keys: %s", strings.Join(diag.ConfigKeys, ","))
			}
		} else {
			t.Logf("MFA diagnostics unavailable (unexpected: Relink should always enrich this error)")
		}
	case errors.Is(err, ErrInvalidStartURL):
		// 03.3-21: Microsoft auth completed, but the final portal page had
		// no valid InscripcionAsignatura enrollment link. Only these five
		// non-sensitive fields ever cross into this log line -- never
		// data-linkid/password/email/the full HTML/the query string. See
		// the two hypotheses documented in this file's header comment.
		if diag, ok := StartURLDiagnosticsFrom(err); ok {
			tipolinks := strings.Join(diag.DataTipolinkValues, ",")
			if tipolinks == "" {
				tipolinks = "ninguno"
			}
			t.Logf("start URL diagnostics: inscribeteLinkCount=%d dataTipolinkValues=%s bootstrapTabPresent=%t host=%s path=%s",
				diag.InscribeteLinkCount, tipolinks, diag.BootstrapTabPresent, diag.Host, diag.Path)
		} else {
			t.Logf("start URL diagnostics unavailable (unexpected: Relink should always enrich this error)")
		}
		t.Fatalf("Relink failed: %v", err)
	default:
		// internal/sso's errors are fixed sentinels/messages, never
		// interpolated with secrets or full HTML, so it's safe to include
		// err in the failure message.
		t.Fatalf("Relink failed: %v", err)
	}
}
