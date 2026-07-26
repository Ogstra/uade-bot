package sso

// Tests for extractMicrosoftConfigJSON/parseMicrosoftConfig -- pure unit
// tests with no httptest server, since these functions operate directly on
// an HTML string already fetched by the rest of the package.
//
// TestExtractMicrosoftConfigJSONIgnoresBraceSequenceInsideStringValue is the
// adversarial case the plan calls out explicitly: a naive "find the first
// '};'" regex/scan would truncate the $Config blob early when a field value
// (sCtx here) happens to contain the literal text "};" inside a JSON string.
// The balanced-brace/string-aware scanner must not fall into that trap.

import (
	"errors"
	"strings"
	"testing"
)

const (
	msconfigFixtureURLPost    = "/9188040d-6c67-4c5b-b112-36a304b66dad/login"
	msconfigFixtureSFT        = "flowtoken-fixture-value-does-not-leak"
	msconfigFixtureSCtxPlain  = "ctx-fixture-plain-value"
	msconfigFixtureSCtxBraced = "ctx-fixture-with-close-brace};-embedded"
	msconfigFixtureCanary     = "canary-fixture-value-does-not-leak"
	msconfigFixtureSessionID  = "session-fixture-value-does-not-leak"
)

func msConfigScriptHTML(urlPost, sft, sctx, canary, sessionID string) string {
	return `<html><head><script type="text/javascript">
//<![CDATA[
$Config={"urlPost":"` + urlPost + `","sFT":"` + sft + `","sCtx":"` + sctx + `","canary":"` + canary + `","sessionId":"` + sessionID + `","extraNestedField":{"a":1,"b":[1,2,3]},"anotherIgnoredField":true};
$Config.otherStuff = doSomethingElse();
//]]>
</script></head><body></body></html>`
}

func TestExtractMicrosoftConfigJSONIgnoresBraceSequenceInsideStringValue(t *testing.T) {
	html := msConfigScriptHTML(msconfigFixtureURLPost, msconfigFixtureSFT, msconfigFixtureSCtxBraced, msconfigFixtureCanary, msconfigFixtureSessionID)

	raw, err := extractMicrosoftConfigJSON(html)
	if err != nil {
		t.Fatalf("extractMicrosoftConfigJSON error: %v", err)
	}

	// A naive scan that stops at the first literal "};" would truncate mid
	// string, well before the object's real closing brace -- confirm the
	// full object (including the fields declared after sCtx) was captured.
	if !strings.Contains(raw, `"sessionId":"`+msconfigFixtureSessionID+`"`) {
		t.Fatalf("extracted JSON truncated before sessionId field: %q", raw)
	}
	if !strings.HasSuffix(raw, "}") {
		t.Fatalf("extracted JSON does not end at the real closing brace: %q", raw)
	}

	cfg, err := parseMicrosoftConfig(html)
	if err != nil {
		t.Fatalf("parseMicrosoftConfig error: %v", err)
	}
	if cfg.SCtx != msconfigFixtureSCtxBraced {
		t.Fatalf("SCtx = %q, want %q (value containing literal \"};\" must survive intact)", cfg.SCtx, msconfigFixtureSCtxBraced)
	}
}

func TestExtractMicrosoftConfigJSONMissingMarkerFailsClosed(t *testing.T) {
	html := `<html><body>no config here</body></html>`
	if _, err := extractMicrosoftConfigJSON(html); !errors.Is(err, ErrMicrosoftFormNotFound) {
		t.Fatalf("err = %v, want ErrMicrosoftFormNotFound", err)
	}
}

func TestExtractMicrosoftConfigJSONNoOpeningBraceFailsClosed(t *testing.T) {
	html := `<html><body><script>$Config;doStuff();</script></body></html>`
	if _, err := extractMicrosoftConfigJSON(html); !errors.Is(err, ErrMicrosoftFormNotFound) {
		t.Fatalf("err = %v, want ErrMicrosoftFormNotFound", err)
	}
}

func TestExtractMicrosoftConfigJSONNeverClosesFailsClosed(t *testing.T) {
	html := `<html><body><script>$Config={"urlPost":"/x","sFT":"y"` // no closing brace at all
	if _, err := extractMicrosoftConfigJSON(html); !errors.Is(err, ErrMicrosoftFormNotFound) {
		t.Fatalf("err = %v, want ErrMicrosoftFormNotFound", err)
	}
}

func TestParseMicrosoftConfigHappyPath(t *testing.T) {
	html := msConfigScriptHTML(msconfigFixtureURLPost, msconfigFixtureSFT, msconfigFixtureSCtxPlain, msconfigFixtureCanary, msconfigFixtureSessionID)
	cfg, err := parseMicrosoftConfig(html)
	if err != nil {
		t.Fatalf("parseMicrosoftConfig error: %v", err)
	}
	if cfg.URLPost != msconfigFixtureURLPost {
		t.Fatalf("URLPost = %q, want %q", cfg.URLPost, msconfigFixtureURLPost)
	}
	if cfg.SFT != msconfigFixtureSFT {
		t.Fatalf("SFT = %q, want %q", cfg.SFT, msconfigFixtureSFT)
	}
	if cfg.SCtx != msconfigFixtureSCtxPlain {
		t.Fatalf("SCtx = %q, want %q", cfg.SCtx, msconfigFixtureSCtxPlain)
	}
	if cfg.Canary != msconfigFixtureCanary {
		t.Fatalf("Canary = %q, want %q", cfg.Canary, msconfigFixtureCanary)
	}
	if cfg.SessionID != msconfigFixtureSessionID {
		t.Fatalf("SessionID = %q, want %q", cfg.SessionID, msconfigFixtureSessionID)
	}
}

func TestParseMicrosoftConfigMalformedJSONFailsClosed(t *testing.T) {
	html := `<html><body><script>$Config={"urlPost":"/x","sFT":,};</script></body></html>`
	if _, err := parseMicrosoftConfig(html); !errors.Is(err, ErrMicrosoftFormNotFound) {
		t.Fatalf("err = %v, want ErrMicrosoftFormNotFound", err)
	}
}

func TestParseMicrosoftConfigMissingRequiredFieldsFailsClosed(t *testing.T) {
	cases := []struct {
		name string
		html string
	}{
		{
			name: "missing urlPost",
			html: msConfigScriptHTML("", msconfigFixtureSFT, msconfigFixtureSCtxPlain, msconfigFixtureCanary, msconfigFixtureSessionID),
		},
		{
			name: "missing sFT",
			html: msConfigScriptHTML(msconfigFixtureURLPost, "", msconfigFixtureSCtxPlain, msconfigFixtureCanary, msconfigFixtureSessionID),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseMicrosoftConfig(tc.html); !errors.Is(err, ErrMicrosoftFormNotFound) {
				t.Fatalf("err = %v, want ErrMicrosoftFormNotFound", err)
			}
		})
	}
}

// TestParseMicrosoftConfigErrorNeverLeaksSecrets confirms that none of the
// fixture's synthetic secret-shaped values ever appear inside an error's
// .Error() string -- internal/sso's errors are fixed sentinels, never
// interpolated with raw $Config content.
func TestParseMicrosoftConfigErrorNeverLeaksSecrets(t *testing.T) {
	html := msConfigScriptHTML("", msconfigFixtureSFT, msconfigFixtureSCtxBraced, msconfigFixtureCanary, msconfigFixtureSessionID)
	_, err := parseMicrosoftConfig(html)
	if err == nil {
		t.Fatal("expected an error for a $Config missing urlPost")
	}
	msg := err.Error()
	for _, secret := range []string{
		msconfigFixtureSFT,
		msconfigFixtureSCtxBraced,
		msconfigFixtureCanary,
		msconfigFixtureSessionID,
	} {
		if strings.Contains(msg, secret) {
			t.Fatalf("error message leaks a synthetic secret value: %q contains %q", msg, secret)
		}
	}
}
