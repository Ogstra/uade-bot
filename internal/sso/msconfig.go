package sso

// $Config is HTML served by a third party (login.microsoftonline.com, the
// "ConvergedSignIn"/ests login pattern). Microsoft's raw HTML for the
// email/password step never contains a server-rendered <form>/<input> --
// its real login form is assembled client-side by JavaScript from a blob
// named $Config embedded in a <script> tag. This was confirmed live during
// the 03.3-17 checkpoint attempt (see
// 03.3-17-live-verification-notes.md, "Paso 3"): the raw fetch()'d HTML had
// zero <input> elements, yet the $Config blob carried everything needed to
// build the real POST (urlPost/sFT/sCtx/canary/sessionId).
//
// Because $Config comes from an untrusted third party, extraction/parsing
// here must fail closed on any malformed or incomplete blob -- never panic,
// never guess a default for a required field, and never let the raw JSON or
// its decoded values leak into an error message.

import (
	"encoding/json"
	"sort"
	"strings"
)

// microsoftConfig captures only the five $Config fields internal/sso's
// Microsoft login submit needs. The real blob carries dozens of other
// fields (telemetry, feature flags, etc.); json.Unmarshal silently drops
// anything without a matching tag here, which is the intended way those
// extra fields get discarded.
type microsoftConfig struct {
	URLPost   string `json:"urlPost"`
	SFT       string `json:"sFT"`
	SCtx      string `json:"sCtx"`
	Canary    string `json:"canary"`
	SessionID string `json:"sessionId"`
}

// extractMicrosoftConfigJSON locates the first "$Config" literal in html,
// finds the next '{' after it, and scans forward counting brace depth --
// while tracking whether the scan is currently inside a JSON string literal
// (toggled on each unescaped '"', skipping the character right after a
// backslash while inside a string) so a '{' or '}' that happens to appear
// inside a string value is never mistaken for real object structure. It
// returns the full substring from the opening '{' to its matching closing
// '}' (both included).
//
// This deliberately does NOT stop at the first literal "};" it sees: a
// naive scan like that would truncate the blob early if a field value (for
// example sCtx) happens to contain that exact two-character sequence inside
// its own string value -- see
// TestExtractMicrosoftConfigJSONIgnoresBraceSequenceInsideStringValue.
func extractMicrosoftConfigJSON(html string) (string, error) {
	markerIdx := strings.Index(html, "$Config")
	if markerIdx < 0 {
		return "", ErrMicrosoftFormNotFound
	}
	rest := html[markerIdx:]

	start := strings.IndexByte(rest, '{')
	if start < 0 {
		return "", ErrMicrosoftFormNotFound
	}

	depth := 0
	inString := false
	escapedNext := false
	for i := start; i < len(rest); i++ {
		c := rest[i]

		if inString {
			switch {
			case escapedNext:
				escapedNext = false
			case c == '\\':
				escapedNext = true
			case c == '"':
				inString = false
			}
			continue
		}

		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return rest[start : i+1], nil
			}
		}
	}

	// Ran off the end of the string without depth returning to 0 (or without
	// ever seeing a string-closing quote) -- malformed/truncated blob, fail
	// closed rather than returning a partial object.
	return "", ErrMicrosoftFormNotFound
}

// parseMicrosoftConfig extracts and decodes the $Config blob out of html.
// It fails closed (ErrMicrosoftFormNotFound, no panic) on extraction
// failure, on invalid JSON, and on the two fields with no safe fallback for
// the POST to make sense (URLPost, SFT) being empty after decoding. It never
// interpolates the raw JSON or any decoded field value into the returned
// error.
func parseMicrosoftConfig(html string) (microsoftConfig, error) {
	raw, err := extractMicrosoftConfigJSON(html)
	if err != nil {
		return microsoftConfig{}, err
	}

	var cfg microsoftConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return microsoftConfig{}, ErrMicrosoftFormNotFound
	}

	if cfg.URLPost == "" || cfg.SFT == "" {
		return microsoftConfig{}, ErrMicrosoftFormNotFound
	}

	return cfg, nil
}

// extractMicrosoftConfigKeyNames returns the sorted key NAMES present in the
// last $Config blob found in html -- never the values behind them. Per D-06
// (03.3-CONTEXT.md), the key names of a third party's $Config are Microsoft's
// own internal field identifiers, not secrets, and are safe to log; the
// VALUES behind those keys never are, and this function never returns them.
// It reuses extractMicrosoftConfigJSON (the same balanced-brace/string-aware
// scanner parseMicrosoftConfig uses) and decodes into a generic
// map[string]json.RawMessage -- unlike parseMicrosoftConfig's 5-field
// microsoftConfig struct, every key present in the blob matters here, not
// just the ones submitMicrosoftLogin needs. It fails closed to nil (never
// panics) when there is no $Config marker at all or when the JSON is
// malformed.
func extractMicrosoftConfigKeyNames(html string) []string {
	raw, err := extractMicrosoftConfigJSON(html)
	if err != nil {
		return nil
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return nil
	}

	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
