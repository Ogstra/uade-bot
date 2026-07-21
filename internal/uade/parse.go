package uade

import (
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

type ParseResult struct {
	Rows                             []Vacancy
	MatchedRowCount, InvalidRowCount int
	ResultsContainerDetected         bool
}

var dayMarkers = map[string]string{"LU": "hiddenLU", "MA": "hiddenMA", "MI": "hiddenMI", "JU": "hiddenJU", "VI": "hiddenVI", "SA": "hiddenSA"}

func ParseResults(html string) ParseResult {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return ParseResult{}
	}
	result := ParseResult{ResultsContainerDetected: doc.Find("table.grillaInscripcion, table#results").Length() > 0}
	doc.Find("tr.row_central, tr.row_recoleta, tr.rowTagueadoNuevo").Each(func(_ int, row *goquery.Selection) {
		result.MatchedRowCount++
		cuposText := strings.TrimSpace(row.Find("td.tdvacantes").First().Text())
		cupos, err := strconv.Atoi(cuposText)
		if err != nil {
			result.InvalidRowCount++
			return
		}
		dias := make([]string, 0, len(dayMarkers))
		for day, marker := range dayMarkers {
			found := false
			row.Find("input[id*='" + marker + "']").EachWithBreak(func(_ int, s *goquery.Selection) bool {
				if value, ok := s.Attr("value"); ok && value == "True" {
					found = true
					return false
				}
				return true
			})
			if found {
				dias = append(dias, day)
			}
		}
		result.Rows = append(result.Rows, Vacancy{Turno: strings.TrimSpace(row.Find("td.tdTurno").First().Text()), Sede: strings.TrimSpace(row.Find("td.tdSede").First().Text()), Horario: strings.TrimSpace(row.Find("td.tdHorario").First().Text()), Dias: strings.Join(dias, ","), Cupos: cupos})
	})
	return result
}

func FilterVacancies(rows []Vacancy, excludedSedes, requestedDays []string) []Vacancy {
	excluded, wanted := map[string]bool{}, map[string]bool{}
	for _, s := range excludedSedes {
		excluded[s] = true
	}
	for _, d := range requestedDays {
		wanted[d] = true
	}
	out := make([]Vacancy, 0, len(rows))
	for _, row := range rows {
		if excluded[row.Sede] || row.Cupos <= 0 {
			continue
		}
		overlap := false
		for _, d := range strings.Split(row.Dias, ",") {
			if wanted[d] {
				overlap = true
			}
		}
		if overlap {
			out = append(out, row)
		}
	}
	return out
}
