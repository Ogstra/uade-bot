package uade

import (
	"context"
	"errors"
	"github.com/PuerkitoBio/goquery"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

type FormState struct{ Fields url.Values }

type SearchFilters struct {
	MateriaCodigo string
	Ofrecimiento  string
	Turno         string
	Dias          []string
}

type SearchForm struct {
	Action        string
	Fields        url.Values
	MateriaNombre string
}

// BuildMateriaCatalogForm mirrors the WebForms __doPostBack used when the
// initial enrollment page has not rendered the materia checkboxes yet.
func BuildMateriaCatalogForm(html string) (SearchForm, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return SearchForm{}, err
	}
	trigger := doc.Find("[id$=btnSeleccionarMaterias], [name$='$btnSeleccionarMaterias']").First()
	if trigger.Length() != 1 {
		return SearchForm{}, errors.New("webforms materia trigger missing")
	}
	form := trigger.Closest("form")
	if form.Length() != 1 {
		return SearchForm{}, errors.New("webforms form missing")
	}
	target, _ := trigger.Attr("name")
	if target == "" {
		if href, ok := trigger.Attr("href"); ok {
			const marker = "__doPostBack('"
			if start := strings.Index(href, marker); start >= 0 {
				rest := href[start+len(marker):]
				if end := strings.Index(rest, "'"); end >= 0 {
					target = rest[:end]
				}
			}
		}
	}
	if target == "" {
		if id, ok := trigger.Attr("id"); ok {
			target = strings.ReplaceAll(id, "_", "$")
		}
	}
	if target == "" {
		return SearchForm{}, errors.New("webforms materia trigger missing")
	}
	fields := serializeSuccessfulControls(form, nil)
	fields.Set("__EVENTTARGET", target)
	fields.Set("__EVENTARGUMENT", "")
	form.Find("input[type=hidden][name$='$ScriptManager1']").First().Each(func(_ int, input *goquery.Selection) {
		if name, ok := input.Attr("name"); ok {
			fields.Set(name, "ctl00$UpdatePanelContenido|"+target)
		}
	})
	action, _ := form.Attr("action")
	return SearchForm{Action: action, Fields: fields}, nil
}

var offeringValues = map[string]string{"curricular": "145", "optativa": "146"}
var daySuffixes = map[string]string{"LU": "chkLunes", "MA": "chkMartes", "MI": "chkMiercoles", "JU": "chkJueves", "VI": "chkViernes", "SA": "chkSabado"}

func ExtractFormState(html string) (FormState, error) {
	doc, e := goquery.NewDocumentFromReader(strings.NewReader(html))
	if e != nil {
		return FormState{}, e
	}
	f := url.Values{}
	doc.Find("input[type=hidden]").Each(func(_ int, s *goquery.Selection) {
		n, ok := s.Attr("name")
		if !ok || n == "" {
			return
		}
		v, _ := s.Attr("value")
		f.Set(n, v)
	})
	if len(f) == 0 {
		return FormState{}, errors.New("webforms hidden state missing")
	}
	return FormState{Fields: f}, nil
}

// BuildSearchForm mirrors the successful-control serialization used by the
// Node oracle. Remote markup is selected semantically; missing or ambiguous
// controls fail closed instead of submitting a guessed query.
func BuildSearchForm(html string, filters SearchFilters) (SearchForm, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return SearchForm{}, err
	}
	if filters.MateriaCodigo == "" || offeringValues[filters.Ofrecimiento] == "" || filters.Turno == "" || len(filters.Dias) == 0 {
		return SearchForm{}, errors.New("webforms filters invalid")
	}

	var submit *goquery.Selection
	doc.Find("input[type=submit], input[type=image], button[type=submit], button:not([type])").Each(func(_ int, s *goquery.Selection) {
		label, _ := s.Attr("value")
		if label == "" {
			label = s.Text()
		}
		if strings.EqualFold(strings.TrimSpace(label), "Buscar") {
			if submit == nil {
				submit = s
			} else {
				submit = &goquery.Selection{}
			}
		}
	})
	if submit == nil || submit.Length() != 1 {
		return SearchForm{}, errors.New("webforms search submit missing or ambiguous")
	}
	form := submit.Closest("form")
	if form.Length() != 1 {
		return SearchForm{}, errors.New("webforms form missing")
	}

	type materiaMatch struct{ name, value, title string }
	var materia *materiaMatch
	form.Find("input[type=checkbox][id*=chkSeleccionar]").Each(func(_ int, s *goquery.Selection) {
		rowText := strings.Join(strings.Fields(s.Closest("tr").Text()), " ")
		if !strings.Contains(rowText, filters.MateriaCodigo) {
			return
		}
		name, ok := s.Attr("name")
		if !ok || name == "" {
			return
		}
		value, ok := s.Attr("value")
		if !ok {
			value = "on"
		}
		title := strings.TrimSpace(strings.TrimPrefix(rowText[strings.Index(rowText, filters.MateriaCodigo)+len(filters.MateriaCodigo):], ":"))
		candidate := &materiaMatch{name: name, value: value, title: title}
		if materia == nil {
			materia = candidate
		} else {
			materia = &materiaMatch{}
		}
	})
	if materia == nil || materia.name == "" {
		return SearchForm{}, errors.New("webforms materia missing or ambiguous")
	}

	offering := offeringValues[filters.Ofrecimiento]
	var offeringName string
	form.Find("input[type=radio]").Each(func(_ int, s *goquery.Selection) {
		if value, _ := s.Attr("value"); value == offering {
			offeringName, _ = s.Attr("name")
		}
	})
	if offeringName == "" {
		return SearchForm{}, errors.New("webforms offering missing")
	}

	var turnoName, turnoValue string
	form.Find("select[id$=cboTurno], select[name$='$cboTurno'], select#turno").First().Each(func(_ int, s *goquery.Selection) {
		turnoName, _ = s.Attr("name")
		s.Find("option").Each(func(_ int, option *goquery.Selection) {
			if strings.EqualFold(strings.TrimSpace(option.Text()), strings.TrimSpace(filters.Turno)) {
				turnoValue, _ = option.Attr("value")
			}
		})
	})
	if turnoName == "" || turnoValue == "" {
		return SearchForm{}, errors.New("webforms turno missing")
	}

	fields := serializeSuccessfulControls(form, submit)
	for name := range fields {
		if strings.Contains(name, "chkSeleccionar") || name == offeringName || name == turnoName {
			fields.Del(name)
		}
	}
	fields.Set(materia.name, materia.value)
	fields.Set(offeringName, offering)
	fields.Set(turnoName, turnoValue)
	for day, suffix := range daySuffixes {
		control := form.Find("input[id$=" + suffix + "], input[name$='$" + suffix + "']").First()
		name, exists := control.Attr("name")
		fields.Del(name)
		if contains(filters.Dias, day) {
			if !exists || name == "" {
				return SearchForm{}, errors.New("webforms day control missing")
			}
			value, ok := control.Attr("value")
			if !ok {
				value = "on"
			}
			fields.Set(name, value)
		}
	}
	if name, ok := submit.Attr("name"); ok && name != "" {
		value, _ := submit.Attr("value")
		fields.Set(name, value)
	}
	action, _ := form.Attr("action")
	return SearchForm{Action: action, Fields: fields, MateriaNombre: materia.title}, nil
}

func serializeSuccessfulControls(form, chosenSubmit *goquery.Selection) url.Values {
	fields := url.Values{}
	form.Find("input, select, textarea, button").Each(func(_ int, control *goquery.Selection) {
		name, ok := control.Attr("name")
		if !ok || name == "" || control.Is("[disabled]") {
			return
		}
		tag := goquery.NodeName(control)
		typ, _ := control.Attr("type")
		typ = strings.ToLower(typ)
		switch {
		case tag == "select":
			selected := control.Find("option[selected]")
			if selected.Length() == 0 {
				selected = control.Find("option").First()
			}
			selected.Each(func(_ int, option *goquery.Selection) {
				value, ok := option.Attr("value")
				if !ok {
					value = option.Text()
				}
				fields.Add(name, value)
			})
		case tag == "textarea":
			fields.Add(name, control.Text())
		case typ == "checkbox" || typ == "radio":
			if control.Is("[checked]") {
				value, ok := control.Attr("value")
				if !ok {
					value = "on"
				}
				fields.Add(name, value)
			}
		case typ == "submit" || typ == "image":
			if chosenSubmit != nil && control.Get(0) == chosenSubmit.Get(0) {
				value, _ := control.Attr("value")
				fields.Add(name, value)
			}
		case typ == "button" || typ == "reset" || typ == "file":
			return
		default:
			value, _ := control.Attr("value")
			fields.Add(name, value)
		}
	})
	return fields
}

func VerifyReflectedSearch(html string, filters SearchFilters) bool {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil || doc.Find("form").Length() == 0 {
		return false
	}
	checked := doc.Find("input[type=checkbox][id*=chkSeleccionar][checked]").First()
	if checked.Length() == 0 || !strings.Contains(strings.Join(strings.Fields(checked.Closest("tr").Text()), " "), filters.MateriaCodigo) {
		return false
	}
	offering := doc.Find("input[type=radio][checked]").FilterFunction(func(_ int, s *goquery.Selection) bool {
		value, _ := s.Attr("value")
		return value == "145" || value == "146"
	}).First()
	value, _ := offering.Attr("value")
	if value != offeringValues[filters.Ofrecimiento] {
		return false
	}
	turno := strings.TrimSpace(doc.Find("select#turno option[selected], select[id$=cboTurno] option[selected], select[name$='$cboTurno'] option[selected]").First().Text())
	if !strings.EqualFold(turno, strings.TrimSpace(filters.Turno)) {
		return false
	}
	actual := []string{}
	for day, suffix := range daySuffixes {
		if doc.Find("input[id$="+suffix+"][checked], input[name$='$"+suffix+"'][checked]").Length() > 0 {
			actual = append(actual, day)
		}
	}
	expected := append([]string(nil), filters.Dias...)
	sort.Strings(actual)
	sort.Strings(expected)
	return strings.Join(actual, ",") == strings.Join(expected, ",")
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
func (c *Client) Postback(ctx context.Context, path string, state FormState, fields map[string]string) (string, error) {
	return c.PostbackWithCredentials(ctx, path, state, fields, "", "")
}

func (c *Client) PostbackWithCredentials(ctx context.Context, path string, state FormState, fields map[string]string, username, password string) (string, error) {
	body := url.Values{}
	for k, v := range state.Fields {
		if len(v) > 0 {
			body.Set(k, v[0])
		}
	}
	for k, v := range fields {
		body.Set(k, v)
	}
	target, e := c.resolve(path)
	if e != nil {
		return "", e
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(body.Encode()))
	if e != nil {
		return "", e
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", c.AllowedOrigin)
	req.Header.Set("Referer", target)
	if username != "" || password != "" {
		req.SetBasicAuth(username, password)
	}
	resp, e := c.HTTP.Do(req)
	if e != nil {
		return "", e
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return "", ErrAuth
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", ErrTransient
	}
	b, e := readBounded(resp.Body, c.MaxBodyBytes)
	if e != nil {
		return "", e
	}
	return string(b), nil
}
