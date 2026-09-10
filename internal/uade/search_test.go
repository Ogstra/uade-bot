package uade

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSearchRunsFullWebFormsFlowAndClassifies(t *testing.T) {
	initial, err := os.ReadFile(filepath.Join("testdata", "webforms", "initial-form.html"))
	if err != nil {
		t.Fatal(err)
	}
	found, err := os.ReadFile(filepath.Join("testdata", "webforms", "postback-found.html"))
	if err != nil {
		t.Fatal(err)
	}
	found = []byte(strings.Replace(string(found), `<table id="results"><tr class="row_central"><td>Física II</td><td>2 vacantes</td></tr></table>`, `<table id="results" class="grillaInscripcion"><tr class="row_central"><td class="tdTurno">MAÑANA</td><td class="tdSede">Lima</td><td class="tdHorario">08:00</td><td class="tdvacantes">2</td><td><input id="x_hiddenLU" value="True"><input id="x_hiddenMI" value="True"></td></tr></table>`, 1))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, password, ok := r.BasicAuth(); !ok || user != "u" || password != "p" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodGet {
			_, _ = w.Write(initial)
			return
		}
		_ = r.ParseForm()
		if r.Form.Get("ctl00$ContentPlaceHolder1$cboTurno") == "" {
			t.Error("turno not submitted")
		}
		_, _ = w.Write(found)
	}))
	defer server.Close()
	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	outcome := client.Search(context.Background(), server.URL, "u", "p", SearchFilters{MateriaCodigo: "3.1.050", Ofrecimiento: "curricular", Turno: "mañana", Dias: []string{"LU", "MI"}}, nil)
	if outcome.Code != OutcomeFound || len(outcome.Vacancies) == 0 {
		t.Fatalf("outcome=%+v", outcome)
	}
	if outcome.Vacancies[0].Codigo != "3.1.050" || !strings.Contains(outcome.Vacancies[0].Materia, "Física") {
		t.Fatalf("vacancy=%+v", outcome.Vacancies[0])
	}
}

func TestSearchPreservesMateriaNameOnVerifiedOutcomes(t *testing.T) {
	initial := readSearchFixture(t, "initial-form.html")
	found := strings.Replace(
		readSearchFixture(t, "postback-found.html"),
		`<table id="results"><tr class="row_central"><td>Física II</td><td>2 vacantes</td></tr></table>`,
		`<table id="results" class="grillaInscripcion"><tr class="row_central"><td class="tdTurno">MAÑANA</td><td class="tdSede">Lima</td><td class="tdHorario">08:00</td><td class="tdvacantes">2</td><td><input id="x_hiddenLU" value="True"><input id="x_hiddenMI" value="True"></td></tr></table>`,
		1,
	)

	tests := []struct {
		name        string
		catalog     string
		result      string
		wantCode    OutcomeCode
		wantNombre  string
		wantVacancy bool
	}{
		{name: "found", catalog: initial, result: found, wantCode: OutcomeFound, wantNombre: "Física II", wantVacancy: true},
		{name: "no vacancies", catalog: initial, result: readSearchFixture(t, "postback-empty.html"), wantCode: OutcomeNoVacancies, wantNombre: "Física II"},
		{name: "catalog without name", catalog: strings.Replace(initial, `<td>Física II</td>`, `<td></td>`, 1), result: readSearchFixture(t, "postback-empty.html"), wantCode: OutcomeNoVacancies},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outcome := runSearchFixture(t, tt.catalog, tt.result)
			if outcome.Code != tt.wantCode {
				t.Fatalf("code=%q, want %q; outcome=%+v", outcome.Code, tt.wantCode, outcome)
			}
			if outcome.MateriaNombre != tt.wantNombre {
				t.Fatalf("materia nombre=%q, want %q", outcome.MateriaNombre, tt.wantNombre)
			}
			if tt.wantVacancy && (len(outcome.Vacancies) != 1 || outcome.Vacancies[0].Materia != tt.wantNombre) {
				t.Fatalf("vacancies=%+v, want one vacancy with materia %q", outcome.Vacancies, tt.wantNombre)
			}
		})
	}
}

func TestResolveMateriaCatalogStopsBeforeSearchPostback(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected catalog POST: %s", r.Method)
		}
		_, _ = w.Write([]byte(`<html><form><select id="turno"><option>Noche</option></select><table><tr><td>3.1.050</td><td>PROGRAMACIÓN 2</td><td><input id="x_chkSeleccionar_0" name="m" type="checkbox"></td></tr></table></form></html>`))
	}))
	defer server.Close()
	client, _ := NewClient(server.URL)
	name, err := client.ResolveMateria(context.Background(), server.URL, "u", "p", "3.1.050")
	if err != nil || name != "PROGRAMACIÓN 2" || requests != 1 {
		t.Fatalf("name=%q err=%v requests=%d", name, err, requests)
	}
}

func TestSearchClassifiesAuthAndStaleStartURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`<html><form></form></html>`))
	}))
	defer server.Close()
	client, _ := NewClient(server.URL)
	filters := SearchFilters{MateriaCodigo: "3.1.050", Ofrecimiento: "curricular", Turno: "Noche", Dias: []string{"LU"}}
	if got := client.Search(context.Background(), server.URL+"/auth", "u", "p", filters, nil); got.Code != OutcomeAuthError {
		t.Fatalf("auth=%+v", got)
	} else if got.MateriaNombre != "" {
		t.Fatalf("auth materia nombre=%q, want empty", got.MateriaNombre)
	}
	if got := client.Search(context.Background(), server.URL+"/stale", "u", "p", filters, nil); got.Code != OutcomeStaleURL {
		t.Fatalf("stale=%+v", got)
	} else if got.MateriaNombre != "" {
		t.Fatalf("stale materia nombre=%q, want empty", got.MateriaNombre)
	}
}

func readSearchFixture(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "webforms", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func runSearchFixture(t *testing.T, catalog, result string) Outcome {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(catalog))
			return
		}
		_, _ = w.Write([]byte(result))
	}))
	defer server.Close()

	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return client.Search(context.Background(), server.URL, "u", "p", SearchFilters{
		MateriaCodigo: "3.1.050",
		Ofrecimiento:  "curricular",
		Turno:         "mañana",
		Dias:          []string{"LU", "MI"},
	}, nil)
}
