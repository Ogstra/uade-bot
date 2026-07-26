package sso

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestExtractStartURLDiagnosticsZeroLinks covers hypothesis A: the final
// portal page has no a.inscribite element at all (no filter by
// data-tipolink -- extractStartURLDiagnostics deliberately scans ALL
// a.inscribite elements, unlike extractStartURL).
func TestExtractStartURLDiagnosticsZeroLinks(t *testing.T) {
	diag := extractStartURLDiagnostics(`<html><body>no inscribete links here</body></html>`)
	if diag.InscribeteLinkCount != 0 {
		t.Fatalf("InscribeteLinkCount = %d, want 0", diag.InscribeteLinkCount)
	}
	if len(diag.DataTipolinkValues) != 0 {
		t.Fatalf("DataTipolinkValues = %v, want empty/nil", diag.DataTipolinkValues)
	}
}

// TestExtractStartURLDiagnosticsSingleLinkNeverLeaksDataLinkid proves the
// core security property: a data-linkid value with a secret-shaped substring
// (param=... -- the real enrollment URL, equivalent to a session credential)
// never appears in any field of StartURLDiagnostics nor in its %+v form.
func TestExtractStartURLDiagnosticsSingleLinkNeverLeaksDataLinkid(t *testing.T) {
	secretSubstring := "param=SECRET123ABC"
	html := fmt.Sprintf(`<html><body>
<a class="link-inscripciones inscribite" data-tipolink="InscripcionAsignatura" data-linkid="https://inscripcionespia.uade.edu.ar/x?%s">¡INSCRIBITE!</a>
</body></html>`, secretSubstring)

	diag := extractStartURLDiagnostics(html)
	if diag.InscribeteLinkCount != 1 {
		t.Fatalf("InscribeteLinkCount = %d, want 1", diag.InscribeteLinkCount)
	}
	if len(diag.DataTipolinkValues) != 1 || diag.DataTipolinkValues[0] != "InscripcionAsignatura" {
		t.Fatalf("DataTipolinkValues = %v, want [InscripcionAsignatura]", diag.DataTipolinkValues)
	}

	serialized := fmt.Sprintf("%+v", diag)
	if strings.Contains(serialized, secretSubstring) {
		t.Fatalf("diagnostics leaked data-linkid secret substring: %q contains %q", serialized, secretSubstring)
	}
}

// TestExtractStartURLDiagnosticsDedupesAndSortsDataTipolink covers hypothesis
// B: several a.inscribite elements of distinct types, including one type
// repeated twice, must produce a deduplicated, alphabetically sorted list of
// data-tipolink values while InscribeteLinkCount keeps the raw element count.
func TestExtractStartURLDiagnosticsDedupesAndSortsDataTipolink(t *testing.T) {
	html := `<html><body>
<a class="inscribite" data-tipolink="CursosMRI" data-linkid="https://inscripcionespia.uade.edu.ar/x?param=aaa">A</a>
<a class="inscribite" data-tipolink="CursosMRI" data-linkid="https://inscripcionespia.uade.edu.ar/x?param=bbb">B</a>
<a class="inscribite" data-tipolink="InscripcionAsignatura" data-linkid="https://inscripcionespia.uade.edu.ar/x?param=ccc">C</a>
</body></html>`

	diag := extractStartURLDiagnostics(html)
	if diag.InscribeteLinkCount != 3 {
		t.Fatalf("InscribeteLinkCount = %d, want 3", diag.InscribeteLinkCount)
	}
	want := []string{"CursosMRI", "InscripcionAsignatura"}
	if len(diag.DataTipolinkValues) != len(want) {
		t.Fatalf("DataTipolinkValues = %v, want %v", diag.DataTipolinkValues, want)
	}
	for i := range want {
		if diag.DataTipolinkValues[i] != want[i] {
			t.Fatalf("DataTipolinkValues = %v, want %v", diag.DataTipolinkValues, want)
		}
	}
}

// TestExtractStartURLDiagnosticsBootstrapTabPresence covers both directions
// of the a[data-toggle="tab"][href="#menu3"] Bootstrap tab detection --
// same selector explore-sso-flow.js confirmed live for locating this tab.
func TestExtractStartURLDiagnosticsBootstrapTabPresence(t *testing.T) {
	t.Run("Present", func(t *testing.T) {
		html := `<html><body><a data-toggle="tab" href="#menu3">Inscribite</a></body></html>`
		if diag := extractStartURLDiagnostics(html); !diag.BootstrapTabPresent {
			t.Fatal("BootstrapTabPresent = false, want true")
		}
	})
	t.Run("Absent", func(t *testing.T) {
		html := `<html><body><p>no bootstrap tab here</p></body></html>`
		if diag := extractStartURLDiagnostics(html); diag.BootstrapTabPresent {
			t.Fatal("BootstrapTabPresent = true, want false")
		}
	})
}

// TestExtractStartURLDiagnosticsNoPanic proves extractStartURLDiagnostics
// never panics on arbitrary/adversarial input: empty string, malformed HTML,
// and a large HTML document with no a.inscribite element at all.
func TestExtractStartURLDiagnosticsNoPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("extractStartURLDiagnostics panicked: %v", r)
		}
	}()

	if diag := extractStartURLDiagnostics(""); diag.InscribeteLinkCount != 0 {
		t.Fatalf("extractStartURLDiagnostics(empty).InscribeteLinkCount = %d, want 0", diag.InscribeteLinkCount)
	}
	if diag := extractStartURLDiagnostics(`<html><body><div class="unclosed`); diag.InscribeteLinkCount != 0 {
		t.Fatalf("extractStartURLDiagnostics(malformed).InscribeteLinkCount = %d, want 0", diag.InscribeteLinkCount)
	}
	large := `<html><body>` + strings.Repeat("<p>no inscribete link here</p>", 10_000) + `</body></html>`
	if diag := extractStartURLDiagnostics(large); diag.InscribeteLinkCount != 0 {
		t.Fatalf("extractStartURLDiagnostics(large).InscribeteLinkCount = %d, want 0", diag.InscribeteLinkCount)
	}
}

// TestStartURLErrorPreservesSentinel proves startURLError never changes
// errors.Is(err, ErrInvalidStartURL) or Error() from what every existing
// caller (Relink's own two return points, TestExtractStartURLMissingLinkFailsClosed
// against the bare sentinel) already depends on.
func TestStartURLErrorPreservesSentinel(t *testing.T) {
	err := newInvalidStartURLError("https://inscripcionespia.uade.edu.ar/final", `<html><body>no link</body></html>`)
	if !errors.Is(err, ErrInvalidStartURL) {
		t.Fatal("errors.Is(err, ErrInvalidStartURL) = false, want true")
	}
	if err.Error() != ErrInvalidStartURL.Error() {
		t.Fatalf("Error() = %q, want byte-identical %q", err.Error(), ErrInvalidStartURL.Error())
	}
}

// TestNewInvalidStartURLErrorHostPathAndQueryLeak confirms Host/Path are
// populated from pageURL (host+path only) and that a synthetic
// session-shaped query string (paramAlumId/cookie) never reaches either
// field.
func TestNewInvalidStartURLErrorHostPathAndQueryLeak(t *testing.T) {
	pageURL := "https://inscripcionespia.uade.edu.ar/InscripcionClaseBuscar.aspx?paramAlumId=987654&cookie=abc"
	html := `<html><body>no link</body></html>`
	err := newInvalidStartURLError(pageURL, html)

	diag, ok := StartURLDiagnosticsFrom(err)
	if !ok {
		t.Fatal("StartURLDiagnosticsFrom(err) ok = false, want true")
	}
	if diag.Host != "inscripcionespia.uade.edu.ar" {
		t.Fatalf("diag.Host = %q, want %q", diag.Host, "inscripcionespia.uade.edu.ar")
	}
	if diag.Path != "/InscripcionClaseBuscar.aspx" {
		t.Fatalf("diag.Path = %q, want %q", diag.Path, "/InscripcionClaseBuscar.aspx")
	}
	for _, leak := range []string{"987654", "cookie", "paramAlumId"} {
		if strings.Contains(diag.Host, leak) || strings.Contains(diag.Path, leak) {
			t.Fatalf("query string leaked into Host/Path: host=%q path=%q (leak=%q)", diag.Host, diag.Path, leak)
		}
	}
}

// TestStartURLDiagnosticsFromRejectsBareSentinelAndNil confirms the pure
// sentinel ErrInvalidStartURL (still used directly by
// TestExtractStartURLMissingLinkFailsClosed against extractStartURL) never
// appears to carry diagnostics, same contract as MFADiagnosticsFrom.
func TestStartURLDiagnosticsFromRejectsBareSentinelAndNil(t *testing.T) {
	if _, ok := StartURLDiagnosticsFrom(ErrInvalidStartURL); ok {
		t.Fatal("StartURLDiagnosticsFrom(ErrInvalidStartURL) ok = true, want false")
	}
	if _, ok := StartURLDiagnosticsFrom(nil); ok {
		t.Fatal("StartURLDiagnosticsFrom(nil) ok = true, want false")
	}
}
