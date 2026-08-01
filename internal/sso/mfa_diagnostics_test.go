package sso

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

// TestExtractAADSTSCode covers extractAADSTSCode's three required
// behaviors: bare-token extraction even when a phrase that interpolates a
// username sits right next to the code (never leaking that surrounding
// text), absence of a code, and no panic on any input shape.
func TestExtractAADSTSCode(t *testing.T) {
	t.Run("BareCodeNextToInterpolatedUsername", func(t *testing.T) {
		// Realistic Microsoft error-message shape: code, colon, sentence
		// that interpolates a username right next to it.
		html := `<div id="err">AADSTS50126: Error validando las credenciales de jperez@uade.edu.ar. Intentalo de nuevo.</div>`
		got := extractAADSTSCode(html)
		if got != "AADSTS50126" {
			t.Fatalf("extractAADSTSCode = %q, want %q (must not leak surrounding text)", got, "AADSTS50126")
		}
		if strings.Contains(got, "jperez") {
			t.Fatalf("extractAADSTSCode leaked interpolated username: %q", got)
		}
	})

	t.Run("NoCodePresent", func(t *testing.T) {
		if got := extractAADSTSCode(`<html><body>Ingresá el código de verificación enviado a tu teléfono.</body></html>`); got != "" {
			t.Fatalf("extractAADSTSCode = %q, want empty string", got)
		}
	})

	t.Run("NoPanicOnEmptyOrMarkerlessInput", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("extractAADSTSCode panicked: %v", r)
			}
		}()
		if got := extractAADSTSCode(""); got != "" {
			t.Fatalf("extractAADSTSCode(empty) = %q, want empty string", got)
		}
		large := strings.Repeat("<p>no marker here</p>", 10_000)
		if got := extractAADSTSCode(large); got != "" {
			t.Fatalf("extractAADSTSCode(large no-marker html) = %q, want empty string", got)
		}
		if got := extractAADSTSCode("this page mentions AADSTS without any digits after it"); got != "" {
			t.Fatalf("extractAADSTSCode(marker without digits) = %q, want empty string", got)
		}
	})
}

// TestMFARequiredErrorPreservesSentinel proves mfaRequiredError never
// changes errors.Is(err, ErrMFARequired) or Error() from what every existing
// caller (internal/app/runtime.go, RelinkWithFallback, runtime_test.go's
// fake Runtime.relink) already depends on.
func TestMFARequiredErrorPreservesSentinel(t *testing.T) {
	err := newMFARequiredError("https://login.microsoftonline.com/tenant/login", msNoFormHTML(), 0)
	if !errors.Is(err, ErrMFARequired) {
		t.Fatal("errors.Is(err, ErrMFARequired) = false, want true")
	}
	if err.Error() != ErrMFARequired.Error() {
		t.Fatalf("Error() = %q, want byte-identical %q", err.Error(), ErrMFARequired.Error())
	}
}

// TestNewMFARequiredErrorAndDiagnosticsFrom confirms the four diagnostic
// fields are populated correctly and that a synthetic session-shaped query
// string never reaches Host/Path.
func TestNewMFARequiredErrorAndDiagnosticsFrom(t *testing.T) {
	pageURL := "https://login.microsoftonline.com/common/login?sessionId=abc123&uaid=9987766"
	html := `<div>AADSTS50076: verificación adicional requerida.</div>`
	err := newMFARequiredError(pageURL, html, 2)

	diag, ok := MFADiagnosticsFrom(err)
	if !ok {
		t.Fatal("MFADiagnosticsFrom(err) ok = false, want true")
	}
	if diag.AADSTSCode != "AADSTS50076" {
		t.Fatalf("diag.AADSTSCode = %q, want %q", diag.AADSTSCode, "AADSTS50076")
	}
	if diag.Host != "login.microsoftonline.com" {
		t.Fatalf("diag.Host = %q, want %q", diag.Host, "login.microsoftonline.com")
	}
	if diag.Path != "/common/login" {
		t.Fatalf("diag.Path = %q, want %q", diag.Path, "/common/login")
	}
	if diag.Hops != 2 {
		t.Fatalf("diag.Hops = %d, want 2", diag.Hops)
	}
	if strings.Contains(diag.Host, "sessionId") || strings.Contains(diag.Path, "sessionId") ||
		strings.Contains(diag.Host, "9987766") || strings.Contains(diag.Path, "9987766") {
		t.Fatalf("query string leaked into Host/Path: host=%q path=%q", diag.Host, diag.Path)
	}
	if diag.ConfigKeys != nil {
		t.Fatalf("diag.ConfigKeys = %v, want nil (fixture html has no $Config)", diag.ConfigKeys)
	}

	// Independent cross-check via net/url that the fixture actually carries
	// a query string (i.e. this test would catch a broken fixture, not just
	// a broken implementation).
	u, err2 := url.Parse(pageURL)
	if err2 != nil || u.RawQuery == "" {
		t.Fatal("test setup bug: pageURL must carry a query string")
	}
}

// TestNewMFARequiredErrorPopulatesConfigKeysWhenPresent covers the other
// half of ConfigKeys: when the final page DOES carry a $Config blob (reusing
// msConfigScriptHTML/msconfigFixture* from msconfig_test.go, same package,
// not duplicated here), diag.ConfigKeys must be exactly the sorted key names
// Task 1's extractMicrosoftConfigKeyNames already proved for that fixture --
// and none of the fixture's synthetic secret-shaped VALUES may ever appear
// in the joined result.
func TestNewMFARequiredErrorPopulatesConfigKeysWhenPresent(t *testing.T) {
	pageURL := "https://login.microsoftonline.com/common/login"
	html := msConfigScriptHTML(msconfigFixtureURLPost, msconfigFixtureSFT, msconfigFixtureSCtxBraced, msconfigFixtureCanary, msconfigFixtureSessionID)
	err := newMFARequiredError(pageURL, html, 1)

	diag, ok := MFADiagnosticsFrom(err)
	if !ok {
		t.Fatal("MFADiagnosticsFrom(err) ok = false, want true")
	}
	want := []string{"anotherIgnoredField", "canary", "extraNestedField", "sCtx", "sFT", "sessionId", "urlPost"}
	if len(diag.ConfigKeys) != len(want) {
		t.Fatalf("diag.ConfigKeys = %v, want %v", diag.ConfigKeys, want)
	}
	for i := range want {
		if diag.ConfigKeys[i] != want[i] {
			t.Fatalf("diag.ConfigKeys = %v, want %v", diag.ConfigKeys, want)
		}
	}

	joined := strings.Join(diag.ConfigKeys, ",")
	for _, secret := range []string{msconfigFixtureSFT, msconfigFixtureCanary, msconfigFixtureSessionID} {
		if strings.Contains(joined, secret) {
			t.Fatalf("diag.ConfigKeys leaked a field value: %q contains %q", joined, secret)
		}
	}
}

// TestMFADiagnosticsFromRejectsBareSentinel confirms the pure sentinel
// ErrMFARequired (still used directly by internal/app/runtime_test.go's fake
// Runtime.relink) never appears to carry diagnostics.
func TestMFADiagnosticsFromRejectsBareSentinel(t *testing.T) {
	if _, ok := MFADiagnosticsFrom(ErrMFARequired); ok {
		t.Fatal("MFADiagnosticsFrom(ErrMFARequired) ok = true, want false")
	}
	if _, ok := MFADiagnosticsFrom(nil); ok {
		t.Fatal("MFADiagnosticsFrom(nil) ok = true, want false")
	}
}

// TestIsInvalidCredentialsAADSTSCode is a direct unit test of the
// classification table itself (Relink's end-to-end behavior is covered
// separately by TestRelinkClassifiesAADSTSCodeForInvalidCredentialsVsMFA in
// relink_test.go): every documented invalid-credentials code must return
// true, every documented MFA code plus an empty/unrecognized code must
// return false -- fail closed by default.
func TestIsInvalidCredentialsAADSTSCode(t *testing.T) {
	invalidCreds := []string{"AADSTS50126", "AADSTS50053", "AADSTS50055"}
	for _, code := range invalidCreds {
		if !isInvalidCredentialsAADSTSCode(code) {
			t.Fatalf("isInvalidCredentialsAADSTSCode(%q) = false, want true", code)
		}
	}

	notInvalidCreds := []string{"AADSTS50079", "AADSTS50076", "AADSTS50072", "AADSTS50074", "", "AADSTS99999", "not-a-code"}
	for _, code := range notInvalidCreds {
		if isInvalidCredentialsAADSTSCode(code) {
			t.Fatalf("isInvalidCredentialsAADSTSCode(%q) = true, want false (fail closed toward ErrMFARequired)", code)
		}
	}
}

// TestInvalidCredentialsErrorPreservesSentinel proves invalidCredentialsError
// never changes errors.Is(err, ErrInvalidCredentials) or Error() from what
// internal/app/runtime.go's healStartURL depends on -- mirrors
// TestMFARequiredErrorPreservesSentinel.
func TestInvalidCredentialsErrorPreservesSentinel(t *testing.T) {
	err := newInvalidCredentialsError("https://login.microsoftonline.com/tenant/login", msLoginErrorHTML("AADSTS50126"), 0)
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatal("errors.Is(err, ErrInvalidCredentials) = false, want true")
	}
	if err.Error() != ErrInvalidCredentials.Error() {
		t.Fatalf("Error() = %q, want byte-identical %q", err.Error(), ErrInvalidCredentials.Error())
	}
	if errors.Is(err, ErrMFARequired) {
		t.Fatal("errors.Is(err, ErrMFARequired) = true, want false -- must not also satisfy the MFA sentinel")
	}
}

// TestNewInvalidCredentialsErrorAndDiagnosticsFrom confirms the diagnostic
// fields are populated correctly, mirroring
// TestNewMFARequiredErrorAndDiagnosticsFrom for the invalid-credentials
// sentinel.
func TestNewInvalidCredentialsErrorAndDiagnosticsFrom(t *testing.T) {
	pageURL := "https://login.microsoftonline.com/common/login?sessionId=abc123&uaid=9987766"
	html := `<div>AADSTS50126: Error validando las credenciales.</div>`
	err := newInvalidCredentialsError(pageURL, html, 1)

	diag, ok := InvalidCredentialsDiagnosticsFrom(err)
	if !ok {
		t.Fatal("InvalidCredentialsDiagnosticsFrom(err) ok = false, want true")
	}
	if diag.AADSTSCode != "AADSTS50126" {
		t.Fatalf("diag.AADSTSCode = %q, want %q", diag.AADSTSCode, "AADSTS50126")
	}
	if diag.Host != "login.microsoftonline.com" {
		t.Fatalf("diag.Host = %q, want %q", diag.Host, "login.microsoftonline.com")
	}
	if diag.Path != "/common/login" {
		t.Fatalf("diag.Path = %q, want %q", diag.Path, "/common/login")
	}
	if diag.Hops != 1 {
		t.Fatalf("diag.Hops = %d, want 1", diag.Hops)
	}
	if strings.Contains(diag.Host, "sessionId") || strings.Contains(diag.Path, "sessionId") {
		t.Fatalf("query string leaked into Host/Path: host=%q path=%q", diag.Host, diag.Path)
	}

	// A bare invalidCredentialsError must never satisfy MFADiagnosticsFrom.
	if _, ok := MFADiagnosticsFrom(err); ok {
		t.Fatal("MFADiagnosticsFrom(invalidCredentialsError) ok = true, want false")
	}
}

// TestInvalidCredentialsDiagnosticsFromRejectsBareSentinel confirms the pure
// sentinel ErrInvalidCredentials never appears to carry diagnostics, mirroring
// TestMFADiagnosticsFromRejectsBareSentinel.
func TestInvalidCredentialsDiagnosticsFromRejectsBareSentinel(t *testing.T) {
	if _, ok := InvalidCredentialsDiagnosticsFrom(ErrInvalidCredentials); ok {
		t.Fatal("InvalidCredentialsDiagnosticsFrom(ErrInvalidCredentials) ok = true, want false")
	}
	if _, ok := InvalidCredentialsDiagnosticsFrom(nil); ok {
		t.Fatal("InvalidCredentialsDiagnosticsFrom(nil) ok = true, want false")
	}
}
