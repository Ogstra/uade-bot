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
	initial, err := os.ReadFile(filepath.Join("..", "..", "src", "automation", "__fixtures__", "webforms", "initial-form.html"))
	if err != nil {
		t.Fatal(err)
	}
	found, err := os.ReadFile(filepath.Join("..", "..", "src", "automation", "__fixtures__", "webforms", "postback-found.html"))
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
	}
	if got := client.Search(context.Background(), server.URL+"/stale", "u", "p", filters, nil); got.Code != OutcomeStaleURL {
		t.Fatalf("stale=%+v", got)
	}
}
