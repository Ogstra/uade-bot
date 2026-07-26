package sso

// The 4 subtests below are the regression coverage for the real production
// bug fixed live in Node (commit 1d56530, confirmed live 2026-07-12, see
// src/automation/sso-relink-uat-checklist.md): data-tipolink=
// "InscripcionAsignatura" is NOT unique to the real Asignaturas listing --
// UADE's MRI (Cursos Regulares Intensivos) listing carries the exact same
// value and sits EARLIER in the DOM, so a search unscoped by panel silently
// selects MRI's link instead of the correct one.
//
// Under the code BEFORE this file's panel.go/relink.go fix, subtests 1
// ("Happy") and 2 ("Distractor") already pass "by accident": the unscoped
// filter still finds the single correct data-tipolink="InscripcionAsignatura"
// link in those two fixtures, because the distractor panel in subtest 2 uses
// a DIFFERENT data-tipolink value. Subtests 3 ("MRIRegression") and 4
// ("FailClosedNoAsignaturasPanel") are the ones that actually fail against
// the unfixed code -- they are the real RED evidence for this plan's TDD
// cycle, not a design mistake in only having 2 of 4 subtests fail.
import (
	"errors"
	"testing"
)

func TestExtractStartURL(t *testing.T) {
	const (
		asignaturasLinkID = "https://inscripcionespia.uade.edu.ar/x?param=synthetic-secret-1"
		distractorLinkID  = "https://inscripcionespia.uade.edu.ar/x?param=synthetic-decoy-1"
		mriDecoyLinkID    = "https://inscripcionespia.uade.edu.ar/x?param=synthetic-mri-decoy-1"
	)

	t.Run("Happy", func(t *testing.T) {
		html := `<html><body>
<div class="panel panel-primary">
  <div class="panel-body">
    <div class="row list-group-item_on">
      <span class="lbl-inscripciones">Asignaturas 2do Cuatrimestre 2026</span>
    </div>
    <a class="inscribite" data-tipolink="InscripcionAsignatura" data-linkid="` + asignaturasLinkID + `">¡INSCRIBITE!</a>
  </div>
</div>
</body></html>`

		got, err := extractStartURL(html)
		if err != nil {
			t.Fatalf("extractStartURL error: %v", err)
		}
		if got != asignaturasLinkID {
			t.Fatalf("got %q, want %q", got, asignaturasLinkID)
		}
	})

	t.Run("Distractor", func(t *testing.T) {
		html := `<html><body>
<div class="panel panel-primary">
  <div class="panel-body">
    <div class="row list-group-item_on">
      <span class="lbl-inscripciones">Cambios/Bajas 2do Cuatrimestre 2026</span>
    </div>
    <a class="inscribite" data-tipolink="CambioAsignaturaPack" data-linkid="` + distractorLinkID + `">Cambiar</a>
  </div>
</div>
<div class="panel panel-primary">
  <div class="panel-body">
    <div class="row list-group-item_on">
      <span class="lbl-inscripciones">Asignaturas 2do Cuatrimestre 2026</span>
    </div>
    <a class="inscribite" data-tipolink="InscripcionAsignatura" data-linkid="` + asignaturasLinkID + `">¡INSCRIBITE!</a>
  </div>
</div>
</body></html>`

		got, err := extractStartURL(html)
		if err != nil {
			t.Fatalf("extractStartURL error: %v", err)
		}
		if got != asignaturasLinkID {
			t.Fatalf("got %q, want %q", got, asignaturasLinkID)
		}
	})

	// MRIRegression is the heart of this plan: it reproduces the exact real
	// production bug (MRI panel with the SAME data-tipolink, BEFORE the
	// Asignaturas panel in the DOM). Under the unfixed code, this fails
	// because doc.Find(...).First() picks the MRI link (it appears first in
	// document order).
	t.Run("MRIRegression", func(t *testing.T) {
		html := `<html><body>
<div class="panel panel-primary">
  <div class="panel-body">
    <div class="row list-group-item_on">
      <span class="lbl-inscripciones">Cursos Regulares Intensivos (MRI) 1er Cuatrimestre 2026</span>
    </div>
    <a class="inscribite" data-tipolink="InscripcionAsignatura" data-linkid="` + mriDecoyLinkID + `">¡INSCRIBITE!</a>
  </div>
</div>
<div class="panel panel-primary">
  <div class="panel-body">
    <div class="row list-group-item_on">
      <span class="lbl-inscripciones">Asignaturas 2do Cuatrimestre 2026</span>
    </div>
    <a class="inscribite" data-tipolink="InscripcionAsignatura" data-linkid="` + asignaturasLinkID + `">¡INSCRIBITE!</a>
  </div>
</div>
</body></html>`

		got, err := extractStartURL(html)
		if err != nil {
			t.Fatalf("extractStartURL error: %v", err)
		}
		if got == mriDecoyLinkID {
			t.Fatalf("got the MRI panel's link %q, want the Asignaturas panel's link %q -- regression of the real production bug (commit 1d56530)", got, asignaturasLinkID)
		}
		if got != asignaturasLinkID {
			t.Fatalf("got %q, want %q", got, asignaturasLinkID)
		}
	})

	t.Run("FailClosedNoAsignaturasPanel", func(t *testing.T) {
		html := `<html><body>
<div class="panel panel-primary">
  <div class="panel-body">
    <div class="row list-group-item_on">
      <span class="lbl-inscripciones">Cursos Regulares Intensivos (MRI) 1er Cuatrimestre 2026</span>
    </div>
    <a class="inscribite" data-tipolink="InscripcionAsignatura" data-linkid="` + mriDecoyLinkID + `">¡INSCRIBITE!</a>
  </div>
</div>
</body></html>`

		_, err := extractStartURL(html)
		if !errors.Is(err, ErrInvalidStartURL) {
			t.Fatalf("err = %v, want ErrInvalidStartURL", err)
		}
	})
}
