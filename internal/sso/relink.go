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

	if IsMicrosoftLogin(pageURL) {
		pageURL, html, err = submitMicrosoftLogin(ctx, fetcher, pageURL, html, MicrosoftEmail(user), password)
		if err != nil {
			return Result{}, err
		}
	}

	if IsMicrosoftLogin(pageURL) {
		// Never guess or submit a challenge/code value we weren't given --
		// still on a Microsoft host after the password chain means MFA or
		// another additional-verification step.
		return Result{Manual: true}, ErrMFARequired
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

// formForField locates the Microsoft login form containing fieldName, using
// Microsoft's long-stable static field ids as a fallback selector.
func formForField(doc *goquery.Document, fieldName string) (*goquery.Selection, error) {
	var selector string
	switch fieldName {
	case "loginfmt":
		selector = `input[name="loginfmt"], #i0116`
	case "passwd":
		selector = `input[name="passwd"], #i0118`
	default:
		return nil, ErrMicrosoftFormNotFound
	}
	field := doc.Find(selector).First()
	if field.Length() == 0 {
		return nil, ErrMicrosoftFormNotFound
	}
	form := field.Closest("form")
	if form.Length() == 0 {
		return nil, ErrMicrosoftFormNotFound
	}
	return form, nil
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

func submitMicrosoftForm(ctx context.Context, fetcher boundedFetcher, pageURL, html, fieldName, fieldValue string) (string, string, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return "", "", ErrMicrosoftFormNotFound
	}
	form, err := formForField(doc, fieldName)
	if err != nil {
		return "", "", err
	}
	values := collectFormValues(form)
	values.Set(fieldName, fieldValue)
	target := pageURL
	if action, _ := form.Attr("action"); strings.TrimSpace(action) != "" {
		resolved, rErr := resolveURL(pageURL, action)
		if rErr != nil {
			return "", "", rErr
		}
		target = resolved
	}
	return fetcher.post(ctx, target, values)
}

// submitMicrosoftLogin chains the two Microsoft form submits (email, then
// password) and falls through to continueMicrosoftChain for any
// interstitial/relay page still served on the Microsoft host afterwards.
func submitMicrosoftLogin(ctx context.Context, fetcher boundedFetcher, pageURL, html, email, password string) (string, string, error) {
	nextURL, nextHTML, err := submitMicrosoftForm(ctx, fetcher, pageURL, html, "loginfmt", email)
	if err != nil {
		return "", "", err
	}
	if !IsMicrosoftLogin(nextURL) {
		return nextURL, nextHTML, nil
	}
	nextURL, nextHTML, err = submitMicrosoftForm(ctx, fetcher, nextURL, nextHTML, "passwd", password)
	if err != nil {
		return "", "", err
	}
	if !IsMicrosoftLogin(nextURL) {
		return nextURL, nextHTML, nil
	}
	return continueMicrosoftChain(ctx, fetcher, nextURL, nextHTML)
}

// continueMicrosoftChain auto-continues through interstitials such as
// "Stay signed in?" and any auto-submit relay page, bounded to
// maxMicrosoftHops. It only ever resubmits values the server already served
// as hidden/default fields -- it never fabricates or completes a field it
// wasn't given, so it can never evade a real challenge; a real challenge
// simply exhausts the loop and Relink falls into ErrMFARequired.
func continueMicrosoftChain(ctx context.Context, fetcher boundedFetcher, pageURL, html string) (string, string, error) {
	currentURL, currentHTML := pageURL, html
	for hop := 0; IsMicrosoftLogin(currentURL) && hop < maxMicrosoftHops; hop++ {
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(currentHTML))
		if err != nil {
			return currentURL, currentHTML, nil
		}
		form := doc.Find("form").First()
		if form.Length() == 0 {
			return currentURL, currentHTML, nil
		}
		values := collectFormValues(form)
		target := currentURL
		if action, _ := form.Attr("action"); strings.TrimSpace(action) != "" {
			resolved, rErr := resolveURL(currentURL, action)
			if rErr != nil {
				return "", "", rErr
			}
			target = resolved
		}
		nextURL, nextHTML, err := fetcher.post(ctx, target, values)
		if err != nil {
			return "", "", err
		}
		currentURL, currentHTML = nextURL, nextHTML
	}
	return currentURL, currentHTML, nil
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
