package sso

import (
	"errors"
	"fmt"
	"testing"
)

// closedPortalHTML is the shape the portal takes while enrollment is closed:
// the Asignaturas panel is still rendered (the heading exists) but carries no
// InscripcionAsignatura link inside it. Another panel keeps its own link, so
// a check that ignored the panel scoping would wrongly read this page as
// "enrollment available".
const closedPortalHTML = `<html><body>
<div class="panel panel-primary">
  <div class="lbl-inscripciones">Cursos Regulares Intensivos (MRI) 1er Cuatrimestre 2027</div>
  <a class="inscribite" data-tipolink="InscripcionAsignatura" data-linkid="https://inscripcionespia.uade.edu.ar/x?param=mri">¡INSCRIBITE!</a>
</div>
<div class="panel panel-primary">
  <div class="lbl-inscripciones">Asignaturas 1er Cuatrimestre 2027</div>
  <p>Las inscripciones se encuentran cerradas.</p>
</div>
</body></html>`

// openPortalHTML is the same page while enrollment is open: the Asignaturas
// panel holds its own InscripcionAsignatura link.
const openPortalHTML = `<html><body>
<div class="panel panel-primary">
  <div class="lbl-inscripciones">Asignaturas 1er Cuatrimestre 2027</div>
  <a class="inscribite" data-tipolink="InscripcionAsignatura" data-linkid="https://inscripcionespia.uade.edu.ar/x?param=abc">¡INSCRIBITE!</a>
</div>
</body></html>`

// noPanelHTML has no Asignaturas panel at all. That is NOT a closed-enrollment
// signal: it is the structural-breakage hypothesis (C) the diagnostics comment
// describes, and must stay on the existing invalid-start-URL path so a real
// selector regression is never silently reported to users as "closed".
const noPanelHTML = `<html><body><div class="panel panel-primary">
  <div class="lbl-inscripciones">Cambios/Bajas 1er Cuatrimestre 2027</div>
</div></body></html>`

func TestEnrollmentClosedDetectsPanelWithoutLink(t *testing.T) {
	err := newInvalidStartURLError("https://sso.uade.edu.ar/", closedPortalHTML)
	if !EnrollmentClosed(err) {
		t.Fatalf("EnrollmentClosed = false, want true for a portal whose Asignaturas panel has no enrollment link")
	}
	// The contract for every existing caller must not change.
	if !errors.Is(err, ErrInvalidStartURL) {
		t.Fatalf("errors.Is(err, ErrInvalidStartURL) = false, want true")
	}
}

func TestEnrollmentClosedIgnoresMissingPanel(t *testing.T) {
	if EnrollmentClosed(newInvalidStartURLError("https://sso.uade.edu.ar/", noPanelHTML)) {
		t.Fatalf("EnrollmentClosed = true, want false when no Asignaturas panel exists (structural breakage, not a closed period)")
	}
}

func TestEnrollmentClosedIgnoresUnrelatedErrors(t *testing.T) {
	for name, err := range map[string]error{
		"nil":                   nil,
		"bare sentinel":         ErrInvalidStartURL,
		"invalid credentials":   ErrInvalidCredentials,
		"unrelated":             errors.New("boom"),
		"wrapped bare sentinel": fmt.Errorf("relink: %w", ErrInvalidStartURL),
	} {
		if EnrollmentClosed(err) {
			t.Fatalf("EnrollmentClosed(%s) = true, want false", name)
		}
	}
}

// A page whose Asignaturas panel still has its link never reaches
// newInvalidStartURLError in production, but the classifier must not report
// "closed" for it under any circumstance.
func TestEnrollmentClosedIgnoresOpenPortal(t *testing.T) {
	if EnrollmentClosed(newInvalidStartURLError("https://sso.uade.edu.ar/", openPortalHTML)) {
		t.Fatalf("EnrollmentClosed = true, want false while the Asignaturas panel still has an enrollment link")
	}
}
