package sso

// This file ports a real production bug fix from the Node predecessor
// (src/automation/sso-link.js, commit 1d56530, "fix(03.1): scope the
// InscripcionAsignatura link to the Asignaturas panel by heading text",
// confirmed live 2026-07-12 -- see src/automation/sso-relink-uat-checklist.md,
// matchedPanelLabel:"Asignaturas 2do Cuatrimestre 2026"). Note:
// src/automation/sso-link.js no longer exists in the working tree (deleted
// by commit 44c5e71, "refactor(runtime): remove browser-based relink") --
// its exact selector/regex logic is ported here from that historical commit,
// not reinvented.
//
// data-tipolink="InscripcionAsignatura" is NOT unique to the real
// "Asignaturas" listing -- UADE's MRI (Cursos Regulares Intensivos) listing
// carries the exact same data-tipolink value, and its panel sits EARLIER in
// the DOM, so a search unscoped by panel silently selects MRI's link instead
// of the correct one. The only thing that actually distinguishes the two is
// the panel's own heading text (.lbl-inscripciones), e.g. "Asignaturas 2do
// Cuatrimestre 2026" vs "Cursos Regulares Intensivos (MRI) 1er Cuatrimestre
// 2026". This was the root cause of a real production incident in Node
// (auto-obtained start URL landing on the wrong materias catalog,
// form_drive_failed with unrelated checkboxes) while manually-copied links
// always worked -- the Go port (03.3-14) replicated the exact same bug
// because its reference source (explore-sso-flow.js) predates the Node fix.
// See 03.3-22-PLAN.md for the full gap-closure writeup.

import (
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

const (
	asignaturasPanelSelector          = ".panel.panel-primary"
	asignaturasLabelSelector          = ".lbl-inscripciones"
	inscripcionAsignaturaLinkSelector = `a.inscribite[data-tipolink="InscripcionAsignatura"]`
)

// asignaturasLabelPattern matches "Asignaturas 2do Cuatrimestre 2026" but
// NOT "Cursos Regulares Intensivos (MRI) 1er Cuatrimestre 2026" nor
// "Cambios/Bajas 2do Cuatrimestre 2026" -- same pattern the Node predecessor
// used (getByText(/^Asignaturas/i)).
var asignaturasLabelPattern = regexp.MustCompile(`(?i)^Asignaturas`)

// asignaturasPanel returns the first .panel.panel-primary block whose own
// .lbl-inscripciones descendant text (trimmed) matches asignaturasLabelPattern
// -- deliberately excludes any panel whose heading starts with something
// else (MRI, Cambios/Bajas, etc.) even though some of those panels' links
// share the same data-tipolink value. Never nil; Length() == 0 when no panel
// matches, same contract as any other goquery search already in this
// package. A panel with no .lbl-inscripciones descendant at all never
// matches.
func asignaturasPanel(doc *goquery.Document) *goquery.Selection {
	return doc.Find(asignaturasPanelSelector).FilterFunction(func(_ int, panel *goquery.Selection) bool {
		label := panel.Find(asignaturasLabelSelector).First()
		if label.Length() == 0 {
			return false
		}
		return asignaturasLabelPattern.MatchString(strings.TrimSpace(label.Text()))
	}).First()
}
