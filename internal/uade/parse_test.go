package uade

import "testing"

func TestParseAndFilterResults(t *testing.T) {
	html := `<table class="grillaInscripcion"><tr class="row_central"><td class="tdTurno">Noche</td><td class="tdSede">Monserrat</td><td class="tdHorario">18:00</td><td class="tdvacantes">3</td><td><input id="x_hiddenLU_0" value="True"></td></tr><tr class="rowTagueadoNuevo"><td class="tdTurno">Día</td><td class="tdSede">Recoleta</td><td class="tdHorario">10:00</td><td class="tdvacantes">0</td></tr></table>`
	parsed := ParseResults(html)
	if parsed.MatchedRowCount != 2 || len(parsed.Rows) != 2 || !parsed.ResultsContainerDetected {
		t.Fatalf("%+v", parsed)
	}
	filtered := FilterVacancies(parsed.Rows, []string{"Recoleta"}, []string{"LU"})
	if len(filtered) != 1 || filtered[0].Cupos != 3 {
		t.Fatalf("%+v", filtered)
	}
}

func TestParseDropsMalformedRows(t *testing.T) {
	parsed := ParseResults(`<table id="results"><tr class="row_central"><td class="tdvacantes">?</td></tr></table>`)
	if parsed.InvalidRowCount != 1 || len(parsed.Rows) != 0 {
		t.Fatalf("%+v", parsed)
	}
}
