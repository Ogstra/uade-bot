package sso

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

var ErrMFARequired = errors.New("mfa or additional verification required")
var ErrInvalidStartURL = errors.New("sso did not produce a valid enrollment URL")
var ErrLoginTriggerNotFound = errors.New("sso login trigger not found")
var ErrMicrosoftFormNotFound = errors.New("microsoft login form not found")

// microsoftLoginHost is a package-level var (not const) so tests can
// substitute an httptest server's host without touching any other logic --
// same seam pattern as respondTimeout in internal/discordgateway/listeners.go.
var microsoftLoginHost = "login.microsoftonline.com"

const enrollmentStartHost = "inscripcionespia.uade.edu.ar"

// maxMicrosoftHops bounds the auto-continuation loop (interstitials/relay
// pages) that follow a successful Microsoft password submit.
const maxMicrosoftHops = 4

var loginTriggerPattern = regexp.MustCompile(`(?i)iniciar\s*sesi[oó]n`)

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

// Relink drives the real UADE portal -> Microsoft OIDC -> UADE portal HTTP
// flow (no browser) and returns a fresh inscripcionespia.uade.edu.ar start
// URL. portalURL is the entry point of the SEPARATE enrollment portal
// (typically https://inscripciones.uade.edu.ar/), not an endpoint that
// receives a direct username/password POST.
func Relink(ctx context.Context, client *http.Client, portalURL, user, password string) (Result, error) {
	if client == nil {
		return Result{}, errors.New("sso client is required")
	}
	fetcher, err := newBoundedFetcher(client, portalURL)
	if err != nil {
		return Result{}, err
	}

	pageURL, html, err := fetcher.get(ctx, portalURL)
	if err != nil {
		return Result{}, err
	}

	if !IsMicrosoftLogin(pageURL) {
		pageURL, html, err = followLoginTrigger(ctx, fetcher, pageURL, html)
		if err != nil {
			return Result{}, err
		}
	}

	var hopsUsed int
	if IsMicrosoftLogin(pageURL) {
		pageURL, html, hopsUsed, err = submitMicrosoftLogin(ctx, fetcher, pageURL, html, MicrosoftEmail(user), password)
		if err != nil {
			return Result{}, err
		}
	}

	if IsMicrosoftLogin(pageURL) {
		// Never guess or submit a challenge/code value we weren't given --
		// still on a Microsoft host after the password chain means MFA or
		// another additional-verification step. newMFARequiredError enriches
		// the sentinel with non-sensitive diagnostics (AADSTS code if
		// present, host+path without query, hops consumed) recoverable via
		// MFADiagnosticsFrom, while errors.Is(err, ErrMFARequired) still
		// holds for every existing caller (Unwrap returns the sentinel
		// itself -- see mfa_diagnostics.go).
		return Result{Manual: true}, newMFARequiredError(pageURL, html, hopsUsed)
	}

	startURL, err := extractStartURL(html)
	if err != nil {
		return Result{}, err
	}
	if !ValidStartURL(startURL) {
		return Result{}, ErrInvalidStartURL
	}
	return Result{StartURL: startURL}, nil
}

// RelinkWithFallback keeps browser/manual work lazy: the fallback is invoked
// only for an explicit MFA/additional-verification outcome, never for a
// transport error or a malformed final URL.
func RelinkWithFallback(ctx context.Context, client *http.Client, portalURL, user, password string, fallback Fallback) (Result, error) {
	result, err := Relink(ctx, client, portalURL, user, password)
	if !errors.Is(err, ErrMFARequired) || fallback == nil {
		return result, err
	}
	return fallback(ctx, user, password)
}

// findLoginTrigger locates the portal's own "Iniciar sesión" element -- the
// step confirmed live to exist by explore-sso-flow.js, but not confirmed at
// the raw-HTTP level (the spike used Playwright's role locators). Matches
// the first a/button/input[type=submit] whose visible text (or, for a
// submit input, its value attribute) matches "iniciar sesión".
func findLoginTrigger(doc *goquery.Document) (*goquery.Selection, error) {
	var trigger *goquery.Selection
	doc.Find("a, button, input[type=submit]").EachWithBreak(func(_ int, s *goquery.Selection) bool {
		text := strings.TrimSpace(s.Text())
		if goquery.NodeName(s) == "input" {
			value, _ := s.Attr("value")
			text = strings.TrimSpace(value)
		}
		if loginTriggerPattern.MatchString(text) {
			trigger = s
			return false
		}
		return true
	})
	if trigger == nil {
		return nil, ErrLoginTriggerNotFound
	}
	return trigger, nil
}

// resolveLoginTrigger turns a located trigger element into a navigable
// request: a navigable <a href> is a GET, anything else (button/submit
// input, or a decorative <a> with no real href) is resolved via its
// containing <form> as a POST with that form's own values (a self-post to
// pageURL when the form has no action, common in ADFS/Azure AD).
func resolveLoginTrigger(trigger *goquery.Selection, pageURL string) (method, target string, values url.Values, err error) {
	if goquery.NodeName(trigger) == "a" {
		href, exists := trigger.Attr("href")
		href = strings.TrimSpace(href)
		if exists && href != "" && href != "#" && !strings.HasPrefix(strings.ToLower(href), "javascript:") {
			resolved, rErr := resolveURL(pageURL, href)
			if rErr != nil {
				return "", "", nil, rErr
			}
			return http.MethodGet, resolved, nil, nil
		}
	}
	form := trigger.Closest("form")
	if form.Length() == 0 {
		return "", "", nil, ErrLoginTriggerNotFound
	}
	target = pageURL
	if action, _ := form.Attr("action"); strings.TrimSpace(action) != "" {
		resolved, rErr := resolveURL(pageURL, action)
		if rErr != nil {
			return "", "", nil, rErr
		}
		target = resolved
	}
	return http.MethodPost, target, collectFormValues(form), nil
}

func followLoginTrigger(ctx context.Context, fetcher boundedFetcher, pageURL, html string) (string, string, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return "", "", ErrLoginTriggerNotFound
	}
	trigger, err := findLoginTrigger(doc)
	if err != nil {
		return "", "", err
	}
	method, target, values, err := resolveLoginTrigger(trigger, pageURL)
	if err != nil {
		return "", "", err
	}
	if method == http.MethodGet {
		return fetcher.get(ctx, target)
	}
	return fetcher.post(ctx, target, values)
}

// collectFormValues reunites every named input inside form. This
// automatically captures whatever anti-forgery hidden field the server
// happens to use (__RequestVerificationToken, PPFT, canary, or anything
// else) without needing to know its name ahead of time -- every hidden gets
// resubmitted, not just the two visible fields.
func collectFormValues(form *goquery.Selection) url.Values {
	values := url.Values{}
	form.Find("input[name]").Each(func(_ int, input *goquery.Selection) {
		name, _ := input.Attr("name")
		if name == "" {
			return
		}
		typ, _ := input.Attr("type")
		typ = strings.ToLower(strings.TrimSpace(typ))
		if typ == "checkbox" || typ == "radio" {
			if _, checked := input.Attr("checked"); !checked {
				return
			}
		}
		value, _ := input.Attr("value")
		values.Set(name, value)
	})
	return values
}

// submitMicrosoftLogin replaces the old two-step "find an <input>, submit
// email, then find another <input>, submit password" chain -- confirmed
// live during the 03.3-17 checkpoint attempt that Microsoft's raw HTML
// never serves those <input> elements at all (see
// 03.3-17-live-verification-notes.md, "Paso 3"). Instead it reads the
// $Config blob Microsoft actually ships and sends a single combined POST
// with both credentials plus $Config's own anti-forgery/session values.
func submitMicrosoftLogin(ctx context.Context, fetcher boundedFetcher, pageURL, html, email, password string) (finalURL, finalHTML string, hopsUsed int, err error) {
	cfg, err := parseMicrosoftConfig(html)
	if err != nil {
		return "", "", 0, err
	}

	target, err := resolveURL(pageURL, cfg.URLPost)
	if err != nil {
		return "", "", 0, err
	}

	values := url.Values{}
	// Confirmed by 03.3-17-live-verification-notes.md ($Config extracted
	// live, "Paso 3"): urlPost/sFT/sCtx/canary/sessionId are the only five
	// fields that section verified. login/loginfmt/passwd are the two
	// credential fields the flow exists to submit.
	values.Set("login", email)
	values.Set("loginfmt", email)
	values.Set("passwd", password)
	values.Set("flowToken", cfg.SFT)
	values.Set("ctx", cfg.SCtx)
	values.Set("canary", cfg.Canary)
	values.Set("hpgrequestid", cfg.SessionID)

	// Everything below this line is a SPECULATIVE default, NOT confirmed
	// live -- 03.3-17-live-verification-notes.md only documents the five
	// $Config fields above. These mirror the general "ests" hidden-field
	// pattern other Microsoft/Azure AD login automation tooling reports,
	// but have not been observed against the real UADE tenant. If 03.3-17
	// fails again at this exact step, THIS block is the first place to
	// adjust.
	values.Set("ps", "2")               // credential-type selector: password
	values.Set("psRNGCDefaultType", "") // no alternate credential offered by a headless client
	values.Set("psRNGCEntropy", "")     // no client-side entropy to report
	values.Set("psRNGCSLK", "")         // no saved-login-key from a headless client
	values.Set("PPSX", "")              // no persistent-session continuation token
	values.Set("NewUser", "1")          // marks a fresh login attempt, not a cached account tile
	values.Set("FoundMSAs", "")         // no consumer (MSA) accounts detected; tenant is work/school
	values.Set("fspost", "0")           // not a password-recovery flow
	values.Set("i21", "0")              // inert telemetry counter placeholder
	values.Set("i19", "0")              // inert telemetry counter placeholder
	values.Set("CookieDisclosure", "0") // cookie-consent banner does not apply to a headless client
	values.Set("IsFidoSupported", "1")  // declaring modern support does not trigger extra challenges on the happy path
	values.Set("isSignupPost", "0")     // this is a login, not a signup
	values.Set("DfpArtifact", "")       // no device-fingerprint artifact; a plain net/http client never generates one

	nextURL, nextHTML, err := fetcher.post(ctx, target, values)
	if err != nil {
		return "", "", 0, err
	}
	if !IsMicrosoftLogin(nextURL) {
		return nextURL, nextHTML, 0, nil
	}
	return continueMicrosoftChain(ctx, fetcher, nextURL, nextHTML)
}

// buildMicrosoftContinuePostValues builds the minimal POST body for
// auto-continuing through a $Config-only Microsoft interstitial (no
// server-rendered <form>) -- the same generalization submitMicrosoftLogin
// already applies to the login step itself. Four of the six fields reuse
// exactly the $Config values the server just served (same mapping already
// used by submitMicrosoftLogin: flowToken/ctx/canary/hpgrequestid from
// SFT/SCtx/Canary/SessionID). The remaining two, LoginOptions="1" and
// type="28", are SPECULATIVE and NOT confirmed live -- they mirror the
// general "ests" KMSI ("Stay signed in?") hidden-field pattern other
// Microsoft/Azure AD login automation tooling reports, but have not been
// observed against the real UADE tenant. If the 03.3-17 checkpoint fails
// again at this exact step, THIS block is the first place to adjust (same
// spirit as the speculative block already in submitMicrosoftLogin).
func buildMicrosoftContinuePostValues(cfg microsoftConfig) url.Values {
	values := url.Values{}
	values.Set("flowToken", cfg.SFT)
	values.Set("ctx", cfg.SCtx)
	values.Set("canary", cfg.Canary)
	values.Set("hpgrequestid", cfg.SessionID)
	values.Set("LoginOptions", "1")
	values.Set("type", "28")
	return values
}

// continueMicrosoftChain auto-continues through interstitials such as
// "Stay signed in?" and any auto-submit relay page, bounded to
// maxMicrosoftHops. It only ever resubmits values the server already served
// as hidden/default fields -- it never fabricates or completes a field it
// wasn't given, so it can never evade a real challenge; a real challenge
// simply exhausts the loop and Relink falls into ErrMFARequired.
func continueMicrosoftChain(ctx context.Context, fetcher boundedFetcher, pageURL, html string) (finalURL, finalHTML string, hopsUsed int, err error) {
	currentURL, currentHTML := pageURL, html
	hop := 0
	for ; IsMicrosoftLogin(currentURL) && hop < maxMicrosoftHops; hop++ {
		doc, docErr := goquery.NewDocumentFromReader(strings.NewReader(currentHTML))
		if docErr != nil {
			return currentURL, currentHTML, hop, nil
		}
		form := doc.Find("form").First()
		if form.Length() == 0 {
			return currentURL, currentHTML, hop, nil
		}
		values := collectFormValues(form)
		target := currentURL
		if action, _ := form.Attr("action"); strings.TrimSpace(action) != "" {
			resolved, rErr := resolveURL(currentURL, action)
			if rErr != nil {
				return "", "", hop, rErr
			}
			target = resolved
		}
		nextURL, nextHTML, postErr := fetcher.post(ctx, target, values)
		if postErr != nil {
			return "", "", hop, postErr
		}
		currentURL, currentHTML = nextURL, nextHTML
	}
	return currentURL, currentHTML, hop, nil
}

// extractStartURL reads the enrollment start URL straight out of the
// data-linkid attribute -- no click needed, and no Bootstrap tab activation
// needed either: data-linkid sits in the served HTML regardless of the
// display:none tab-pane state. The selector pins both class and
// data-tipolink because data-tipolink alone is not unique (Phase 3.1 live
// UAT: UADE's MRI listing carries the same data-tipolink value earlier in
// the DOM).
func extractStartURL(html string) (string, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return "", ErrInvalidStartURL
	}
	link := doc.Find(`a.inscribite[data-tipolink="InscripcionAsignatura"]`).First()
	if link.Length() == 0 {
		return "", ErrInvalidStartURL
	}
	startURL, exists := link.Attr("data-linkid")
	if !exists || strings.TrimSpace(startURL) == "" {
		return "", ErrInvalidStartURL
	}
	return startURL, nil
}
