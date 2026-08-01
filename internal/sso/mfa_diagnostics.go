package sso

// This file exists to diagnose a specific reported false positive: the
// second attempt of the 03.3-17 checkpoint returned ErrMFARequired against a
// UADE test account confirmed WITHOUT MFA. Per D-06 (03.3-CONTEXT.md), no
// credential material may ever be requested, seen, or logged by an
// automated agent -- the only safe way to investigate is to have the four
// fields below captured INSIDE the code, then have the human who ran the
// test paste them. All four are deliberately non-sensitive:
//   - AADSTSCode: Microsoft's own public, documented error-code prefix
//     (https://learn.microsoft.com/entra/identity-platform/reference-error-codes)
//     -- not a secret or session token.
//   - Host/Path: captured WITHOUT the query string, which can carry
//     session-shaped values.
//   - Hops: just a counter of continueMicrosoftChain iterations.
//
// None of password/email/sFT/sCtx/canary/sessionId/hpgrequestid/the full
// HTML/the query string are ever captured here.

import (
	"errors"
	"net/url"
	"regexp"
)

// aadstsCodePattern matches Microsoft's public AADSTS error-code prefix,
// same style as loginTriggerPattern already declared in relink.go.
var aadstsCodePattern = regexp.MustCompile(`AADSTS\d+`)

// MFADiagnostics carries exactly the non-sensitive diagnostic fields
// described above. No other field may be added to this struct -- in
// particular never password/email/sFT/sCtx/canary/sessionId/hpgrequestid/
// query string.
type MFADiagnostics struct {
	AADSTSCode string
	Host       string
	Path       string
	Hops       int

	// ConfigKeys is, unlike the four fields above, not a value captured from
	// the page -- it is the list of NAMES (never the values) of the last
	// $Config blob's keys, per D-06 (03.3-CONTEXT.md): a third party's
	// $Config field NAMES are Microsoft's own internal identifiers, not
	// secrets, and are safe to log; the VALUES behind those keys never are.
	// nil when the last page Relink saw had no $Config at all.
	ConfigKeys []string
}

// extractAADSTSCode returns the first bare AADSTS\d+ token found in html, or
// "" if none is present. It never panics on any input (empty string, huge
// HTML, HTML where "AADSTS" appears with no digits after it) and it never
// returns anything beyond the bare token itself -- never the surrounding
// text, which could interpolate a username or other page content.
func extractAADSTSCode(html string) string {
	return aadstsCodePattern.FindString(html)
}

// mfaRequiredError wraps ErrMFARequired with non-sensitive MFA diagnostics
// without changing its observable contract for any existing caller: Error()
// delegates to ErrMFARequired.Error() verbatim, and Unwrap() returns the
// exact sentinel value (not a copy), so errors.Is(err, ErrMFARequired)
// remains true for internal/app/runtime.go, RelinkWithFallback, and every
// existing test.
type mfaRequiredError struct {
	diag MFADiagnostics
}

func (e *mfaRequiredError) Error() string {
	return ErrMFARequired.Error()
}

func (e *mfaRequiredError) Unwrap() error {
	return ErrMFARequired
}

// newMFARequiredError builds the enriched MFA error Relink returns instead
// of the bare ErrMFARequired sentinel. pageURL/html are the final
// values Relink already has in hand at the point it detects the MFA branch
// -- this never issues an extra request to gather more data. A pageURL that
// fails to parse leaves Host/Path at their zero value rather than failing
// this function -- this diagnostic must never block the real error path.
func newMFARequiredError(pageURL, html string, hops int) error {
	diag := MFADiagnostics{
		AADSTSCode: extractAADSTSCode(html),
		Hops:       hops,
		ConfigKeys: extractMicrosoftConfigKeyNames(html),
	}
	if u, err := url.Parse(pageURL); err == nil {
		diag.Host = u.Hostname()
		diag.Path = u.Path
	}
	return &mfaRequiredError{diag: diag}
}

// MFADiagnosticsFrom extracts MFADiagnostics from err if err is (or wraps) a
// *mfaRequiredError. It returns ok=false for any other error, including the
// pure ErrMFARequired sentinel used directly by
// internal/app/runtime_test.go's fake Runtime.relink -- that bare sentinel
// never carries diagnostics.
func MFADiagnosticsFrom(err error) (MFADiagnostics, bool) {
	var mfaErr *mfaRequiredError
	if errors.As(err, &mfaErr) {
		return mfaErr.diag, true
	}
	return MFADiagnostics{}, false
}

// invalidCredentialsAADSTSCodes are Microsoft's own public, documented AADSTS
// error-code prefixes (https://learn.microsoft.com/entra/identity-platform/
// reference-error-codes) that mean the submitted credentials were rejected
// outright -- wrong username/password, an expired password, or a locked
// account -- as opposed to a real MFA/additional-verification code. The
// correct recovery for any of these is asking the user for a NEW PASSWORD,
// never the manual enrollment-link flow ErrMFARequired triggers.
//
// Deliberately NOT included here (these mean real MFA/additional
// verification, and must keep returning ErrMFARequired):
// AADSTS50079/50076/50072/50074.
var invalidCredentialsAADSTSCodes = map[string]bool{
	"AADSTS50126": true, // invalid username or password
	"AADSTS50053": true, // account locked
	"AADSTS50055": true, // password expired
}

// isInvalidCredentialsAADSTSCode reports whether code is one of Microsoft's
// documented invalid-credentials codes. An empty string or any unrecognized
// code (including every known MFA code, and any future/unmapped Microsoft
// error) returns false -- callers must fail closed toward ErrMFARequired for
// anything not explicitly on this list, since incorrectly assuming "not MFA"
// would prompt a user for a password that might actually already be correct.
func isInvalidCredentialsAADSTSCode(code string) bool {
	return invalidCredentialsAADSTSCodes[code]
}

// invalidCredentialsError wraps ErrInvalidCredentials with the same
// non-sensitive MFADiagnostics shape mfaRequiredError already attaches to
// ErrMFARequired, without changing its observable contract for any existing
// caller: Error() delegates to ErrInvalidCredentials.Error() verbatim, and
// Unwrap() returns the exact sentinel value (not a copy), so
// errors.Is(err, ErrInvalidCredentials) holds for every caller.
type invalidCredentialsError struct {
	diag MFADiagnostics
}

func (e *invalidCredentialsError) Error() string {
	return ErrInvalidCredentials.Error()
}

func (e *invalidCredentialsError) Unwrap() error {
	return ErrInvalidCredentials
}

// newInvalidCredentialsError builds the enriched invalid-credentials error
// Relink returns instead of ErrMFARequired when isInvalidCredentialsAADSTSCode
// matches. Mirrors newMFARequiredError field for field -- see that function's
// header comment for the non-sensitivity guarantees each field carries (this
// never issues an extra request, and a pageURL that fails to parse leaves
// Host/Path at their zero value rather than failing this function).
func newInvalidCredentialsError(pageURL, html string, hops int) error {
	diag := MFADiagnostics{
		AADSTSCode: extractAADSTSCode(html),
		Hops:       hops,
		ConfigKeys: extractMicrosoftConfigKeyNames(html),
	}
	if u, err := url.Parse(pageURL); err == nil {
		diag.Host = u.Hostname()
		diag.Path = u.Path
	}
	return &invalidCredentialsError{diag: diag}
}

// InvalidCredentialsDiagnosticsFrom extracts MFADiagnostics from err if err is
// (or wraps) a *invalidCredentialsError. It returns ok=false for any other
// error, including the pure ErrInvalidCredentials sentinel a test double
// might return directly -- that bare sentinel never carries diagnostics.
// Mirrors MFADiagnosticsFrom.
func InvalidCredentialsDiagnosticsFrom(err error) (MFADiagnostics, bool) {
	var credErr *invalidCredentialsError
	if errors.As(err, &credErr) {
		return credErr.diag, true
	}
	return MFADiagnostics{}, false
}
