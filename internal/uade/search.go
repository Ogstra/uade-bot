package uade

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

const (
	OutcomeRateLimited OutcomeCode = "rate_limited"
	OutcomeStaleURL    OutcomeCode = "stale_start_url"
)

// Search executes the complete browserless WebForms GET/catalog/search flow.
// Every ambiguous response fails closed as search_failed.
func (c *Client) Search(ctx context.Context, startURL, username, password string, filters SearchFilters, excludedSedes []string) Outcome {
	initial, err := c.Fetch(ctx, startURL, username, password)
	if err != nil {
		return transportOutcome(err)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(initial))
	if err != nil || doc.Find("select#turno option, select[id$=cboTurno] option, select[name$='$cboTurno'] option").Length() == 0 {
		return Outcome{Code: OutcomeStaleURL}
	}
	catalog := initial
	if doc.Find("input[type=checkbox][id*=chkSeleccionar]").Length() == 0 {
		catalogForm, buildErr := BuildMateriaCatalogForm(initial)
		if buildErr != nil {
			return Outcome{Code: OutcomeTransientErr, Reason: "materia catalog form invalid"}
		}
		state, stateErr := ExtractFormState(initial)
		if stateErr != nil {
			return Outcome{Code: OutcomeTransientErr, Reason: "webforms state missing"}
		}
		catalog, err = c.PostbackWithCredentials(ctx, catalogForm.Action, state, firstValues(catalogForm.Fields), username, password)
		if err != nil {
			return transportOutcome(err)
		}
	}
	searchForm, err := BuildSearchForm(catalog, filters)
	if err != nil {
		return Outcome{Code: OutcomeTransientErr, Reason: "search form invalid"}
	}
	state, err := ExtractFormState(catalog)
	if err != nil {
		return Outcome{Code: OutcomeTransientErr, Reason: "webforms state missing"}
	}
	body, err := c.PostbackWithCredentials(ctx, searchForm.Action, state, firstValues(searchForm.Fields), username, password)
	if err != nil {
		return transportOutcome(err)
	}
	verifiedHTML := body
	if !strings.Contains(strings.ToLower(body), "<html") {
		parsed, failure := ParseDelta(body, 256, 300_000)
		if failure != "" || len(parsed.Panels) == 0 {
			return Outcome{Code: OutcomeTransientErr, Reason: "delta response invalid"}
		}
		verifiedHTML = applyDelta(catalog, searchForm.Fields, parsed)
	}
	if !VerifyReflectedSearch(verifiedHTML, filters) {
		return Outcome{Code: OutcomeTransientErr, Reason: "postback mismatch"}
	}
	parsed := ParseResults(verifiedHTML)
	if !parsed.ResultsContainerDetected || parsed.InvalidRowCount > 0 {
		return Outcome{Code: OutcomeTransientErr, Reason: "results response invalid"}
	}
	rows := FilterVacancies(parsed.Rows, excludedSedes, filters.Dias)
	for i := range rows {
		rows[i].Codigo = filters.MateriaCodigo
		rows[i].Materia = searchForm.MateriaNombre
	}
	outcome := Classify(true, false, false, rows)
	outcome.MateriaNombre = searchForm.MateriaNombre
	return outcome
}

func transportOutcome(err error) Outcome {
	switch {
	case errors.Is(err, ErrAuth):
		return Outcome{Code: OutcomeAuthError}
	case errors.Is(err, ErrRateLimit):
		return Outcome{Code: OutcomeRateLimited}
	default:
		return Outcome{Code: OutcomeTransientErr, Reason: "transport failure"}
	}
}

func firstValues(values url.Values) map[string]string {
	out := make(map[string]string, len(values))
	for key, items := range values {
		if len(items) > 0 {
			out[key] = items[0]
		}
	}
	return out
}

func applyDelta(initial string, submitted url.Values, delta DeltaResult) string {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(initial))
	if err != nil {
		return ""
	}
	doc.Find("input, select, textarea").Each(func(_ int, control *goquery.Selection) {
		name, ok := control.Attr("name")
		if !ok || name == "" {
			return
		}
		values := submitted[name]
		tag := goquery.NodeName(control)
		typeName, _ := control.Attr("type")
		if typeName == "checkbox" || typeName == "radio" {
			control.RemoveAttr("checked")
			value, exists := control.Attr("value")
			if !exists {
				value = "on"
			}
			for _, submittedValue := range values {
				if submittedValue == value {
					control.SetAttr("checked", "checked")
				}
			}
		} else if tag == "select" {
			control.Find("option").Each(func(_ int, option *goquery.Selection) {
				option.RemoveAttr("selected")
				value, exists := option.Attr("value")
				if !exists {
					value = option.Text()
				}
				for _, submittedValue := range values {
					if submittedValue == value {
						option.SetAttr("selected", "selected")
					}
				}
			})
		}
	})
	form := doc.Find("form").First()
	for _, hidden := range delta.Hidden {
		selector := "input[type=hidden][name='" + strings.ReplaceAll(hidden.Name, "'", "\\'") + "']"
		if input := form.Find(selector).First(); input.Length() > 0 {
			input.SetAttr("value", hidden.Value)
		} else {
			form.AppendHtml("<input type=\"hidden\" name=\"" + hidden.Name + "\" value=\"" + hidden.Value + "\">")
		}
	}
	for _, panel := range delta.Panels {
		form.AppendHtml(panel.HTML)
	}
	html, _ := doc.Html()
	return html
}
