package sso

// This file exists to diagnose the specific failure reported in the fourth
// attempt of the 03.3-17 checkpoint (after 03.3-20 resolved the earlier MFA
// false positive): Relink completes Microsoft authentication end to end but
// then fails with ErrInvalidStartURL because the final portal page has no
// a.inscribite[data-tipolink="InscripcionAsignatura"] with a non-empty
// data-linkid. There are two equally plausible hypotheses without more
// evidence: (A) the test account genuinely has no InscripcionAsignatura
// enrollment available right now -- correct fail-closed behavior, not a bug
// -- or (B) a real selector/structure problem. The fields below are exactly
// what's needed to distinguish A from B, and are all safe to log per D-06
// (03.3-CONTEXT.md, same principle mfa_diagnostics.go already applies):
//   - InscribeteLinkCount/DataTipolinkValues: data-tipolink is a public site
//     category label (confirmed live in explore-sso-flow.js and in
//     extractStartURL's own comment, e.g. "InscripcionAsignatura" vs.
//     "Cursos Regulares Intensivos (MRI)") -- not a secret.
//   - BootstrapTabPresent: just a boolean about page structure.
//   - AsignaturasPanelFound/AsignaturasPanelLinkCount (03.3-22): also just a
//     boolean and a plain count, exactly as safe as the fields above --
//     added after 03.3-22 scoped extractStartURL's link search to the
//     Asignaturas panel (panel.go), to distinguish a THIRD hypothesis
//     TestRelink's original two couldn't: (A) no InscripcionAsignatura
//     enrollment at all (InscribeteLinkCount == 0), (B) the panel exists but
//     has no matching link inside it (AsignaturasPanelFound == true,
//     AsignaturasPanelLinkCount == 0), or (C) no Asignaturas panel exists at
//     all (AsignaturasPanelFound == false) even though some OTHER panel
//     (e.g. MRI) has a link sharing the same data-tipolink.
//   - Host/Path: captured WITHOUT the query string, which can carry
//     session-shaped values.
//
// data-linkid (the real enrollment URL, carrying param=, equivalent to a
// session credential) is NEVER read by this file -- unlike the "start url
// length=N" the success path already logs in live_test.go, this file
// deliberately exposes no length or any other derived signal about
// data-linkid at all. That asymmetry is intentional. Nor does this file ever
// read/log the .lbl-inscripciones heading TEXT itself -- only whether a
// panel matching it exists (bool) and how many links sit inside it (int).

import (
	"errors"
	"net/url"
	"sort"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// StartURLDiagnostics carries exactly the non-sensitive diagnostic fields
// described above. No other field may be added to this struct -- in
// particular never data-linkid or the .lbl-inscripciones heading text.
type StartURLDiagnostics struct {
	InscribeteLinkCount       int
	DataTipolinkValues        []string
	BootstrapTabPresent       bool
	AsignaturasPanelFound     bool
	AsignaturasPanelLinkCount int
	Host                      string
	Path                      string
}

// extractStartURLDiagnostics scans html for every a.inscribite element
// (deliberately NOT filtered by data-tipolink="InscripcionAsignatura" --
// unlike extractStartURL, the point here is to see every type present) and
// summarizes it into StartURLDiagnostics. It never panics on any input --
// empty string, malformed HTML, or HTML with no a.inscribite element at all
// all return the zero-ish value with InscribeteLinkCount 0. Host/Path are
// left at their zero value here; newInvalidStartURLError fills them in from
// pageURL separately, mirroring newMFARequiredError's two-step pattern.
func extractStartURLDiagnostics(html string) StartURLDiagnostics {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return StartURLDiagnostics{}
	}

	links := doc.Find("a.inscribite")
	diag := StartURLDiagnostics{
		InscribeteLinkCount: links.Length(),
		BootstrapTabPresent: doc.Find(`a[data-toggle="tab"][href="#menu3"]`).Length() > 0,
	}

	seen := make(map[string]struct{})
	links.Each(func(_ int, s *goquery.Selection) {
		value, _ := s.Attr("data-tipolink")
		seen[value] = struct{}{}
	})
	if len(seen) > 0 {
		values := make([]string, 0, len(seen))
		for v := range seen {
			values = append(values, v)
		}
		sort.Strings(values)
		diag.DataTipolinkValues = values
	}

	// 03.3-22: reuse the same panel-scoping helper extractStartURL itself
	// uses (panel.go), so this diagnostic distinguishes "no Asignaturas
	// panel exists at all" from "the panel exists but has no matching link
	// inside it" -- see this file's header comment.
	panel := asignaturasPanel(doc)
	diag.AsignaturasPanelFound = panel.Length() > 0
	if diag.AsignaturasPanelFound {
		diag.AsignaturasPanelLinkCount = panel.Find(inscripcionAsignaturaLinkSelector).Length()
	}

	return diag
}

// startURLError wraps ErrInvalidStartURL with non-sensitive start-URL
// diagnostics without changing its observable contract for any existing
// caller: Error() delegates to ErrInvalidStartURL.Error() verbatim, and
// Unwrap() returns the exact sentinel value (not a copy), so
// errors.Is(err, ErrInvalidStartURL) remains true for every existing caller
// and test -- same pattern as mfaRequiredError in mfa_diagnostics.go.
type startURLError struct {
	diag StartURLDiagnostics
}

func (e *startURLError) Error() string {
	return ErrInvalidStartURL.Error()
}

func (e *startURLError) Unwrap() error {
	return ErrInvalidStartURL
}

// newInvalidStartURLError builds the enriched start-URL error Relink returns
// instead of the bare ErrInvalidStartURL sentinel. pageURL/html are the
// final values Relink already has in hand at the point it detects the
// failure -- this never issues an extra request to gather more data. A
// pageURL that fails to parse leaves Host/Path at their zero value rather
// than failing this function -- this diagnostic must never block the real
// error path.
func newInvalidStartURLError(pageURL, html string) error {
	diag := extractStartURLDiagnostics(html)
	if u, err := url.Parse(pageURL); err == nil {
		diag.Host = u.Hostname()
		diag.Path = u.Path
	}
	return &startURLError{diag: diag}
}

// StartURLDiagnosticsFrom extracts StartURLDiagnostics from err if err is
// (or wraps) a *startURLError. It returns ok=false for any other error,
// including the pure ErrInvalidStartURL sentinel used directly by
// TestExtractStartURLMissingLinkFailsClosed against extractStartURL, and
// nil.
func StartURLDiagnosticsFrom(err error) (StartURLDiagnostics, bool) {
	var sErr *startURLError
	if errors.As(err, &sErr) {
		return sErr.diag, true
	}
	return StartURLDiagnostics{}, false
}
