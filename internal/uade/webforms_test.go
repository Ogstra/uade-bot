package uade

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestExtractAndPostback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.Write([]byte(`<input type="hidden" name="__VIEWSTATE" value="x">`))
			return
		}
		if r.FormValue("__VIEWSTATE") != "x" {
			t.Fatal("state missing")
		}
		w.Write([]byte("ok"))
	}))
	defer srv.Close()
	c, _ := NewClient(srv.URL)
	s, e := ExtractFormState(`<form><input type="hidden" name="__VIEWSTATE" value="x"></form>`)
	if e != nil {
		t.Fatal(e)
	}
	body, e := c.Postback(context.Background(), "/", s, map[string]string{"go": "1"})
	if e != nil || body != "ok" {
		t.Fatalf("%q %v", body, e)
	}
}

func TestPostbackPreservesAuthHeadersAndBodyLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "u" || password != "p" || r.Header.Get("Origin") == "" || r.Header.Get("Referer") == "" {
			t.Error("postback transport contract missing")
		}
		_, _ = w.Write([]byte("123456"))
	}))
	defer srv.Close()
	c, _ := NewClient(srv.URL)
	c.MaxBodyBytes = 5
	_, err := c.PostbackWithCredentials(context.Background(), "/", FormState{Fields: map[string][]string{"__VIEWSTATE": {"x"}}}, nil, "u", "p")
	if !errors.Is(err, ErrTransient) {
		t.Fatalf("err=%v", err)
	}
}

func TestBuildSearchFormMatchesNodeFixtureContract(t *testing.T) {
	html := readWebFormsFixture(t, "initial-form.html")
	filters := SearchFilters{MateriaCodigo: "3.1.050", Ofrecimiento: "curricular", Turno: "mañana", Dias: []string{"LU", "MI"}}
	form, err := BuildSearchForm(html, filters)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"__VIEWSTATE":       "DUMMY_VIEWSTATE",
		"__EVENTVALIDATION": "DUMMY_EVENTVALIDATION",
		"ctl00$ContentPlaceHolder1$optOfrecimiento": "145",
		"ctl00$ContentPlaceHolder1$cboTurno":        "10152",
		"ctl00$ContentPlaceHolder1$chkLunes":        "on",
		"ctl00$ContentPlaceHolder1$chkMiercoles":    "on",
		"ctl00$ContentPlaceHolder1$btnBuscar":       "Buscar",
	}
	for name, value := range want {
		if got := form.Fields.Get(name); got != value {
			t.Errorf("%s=%q, want %q", name, got, value)
		}
	}
	if form.Fields.Has("disabledField") || form.Fields.Has("uncheckedField") {
		t.Fatal("unsuccessful controls must not be serialized")
	}
	if form.Action != "/InscripcionClaseBuscar.aspx" {
		t.Fatalf("action=%q", form.Action)
	}
}

func TestMateriaNombreUsesAcademicCellAndPreservesLegitimateDigits(t *testing.T) {
	for _, test := range []struct{ code, row, want string }{
		{"1.1.010", `<tr><td>1.1.010</td><td>ELEMENTOS DE ÁLGEBRA Y GEOMETRÍA85EXAMEN FINAL OBLIGATORIO (85EXAMEN FINAL OBLIGATORIO)</td><td>85EXAMEN FINAL OBLIGATORIO</td><td><input id="x_chkSeleccionar_0" name="m1" type="checkbox"></td></tr>`, "ELEMENTOS DE ÁLGEBRA Y GEOMETRÍA"},
		{"2.2.020", `<tr><td>2.2.020</td><td>PROGRAMACIÓN 2</td><td><section>85 EXAMEN FINAL OBLIGATORIO</section></td><td><input id="x_chkSeleccionar_1" name="m2" type="checkbox"></td></tr>`, "PROGRAMACIÓN 2"},
	} {
		html := `<form><table>` + test.row + `</table></form>`
		got, err := ResolveMateriaName(html, test.code)
		if err != nil || got != test.want {
			t.Fatalf("ResolveMateriaName(%s)=%q,%v want %q", test.code, got, err, test.want)
		}
	}
}

func TestReflectedSearchDifferentialFixtures(t *testing.T) {
	filters := SearchFilters{MateriaCodigo: "3.1.050", Ofrecimiento: "curricular", Turno: "mañana", Dias: []string{"LU", "MI"}}
	for _, name := range []string{"postback-found.html", "postback-empty.html"} {
		if !VerifyReflectedSearch(readWebFormsFixture(t, name), filters) {
			t.Errorf("%s should match the Node oracle", name)
		}
	}
	if VerifyReflectedSearch(readWebFormsFixture(t, "postback-mismatch.html"), filters) {
		t.Fatal("mismatched reflected state must fail closed")
	}
}

func readWebFormsFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("../../src/automation/__fixtures__/webforms/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
