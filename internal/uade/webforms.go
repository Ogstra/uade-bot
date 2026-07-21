package uade

import (
	"context"
	"errors"
	"github.com/PuerkitoBio/goquery"
	"net/http"
	"net/url"
	"strings"
)

type FormState struct{ Fields url.Values }

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
func (c *Client) Postback(ctx context.Context, path string, state FormState, fields map[string]string) (string, error) {
	body := url.Values{}
	for k, v := range state.Fields {
		body.Set(k, v)
	}
	for k, v := range fields {
		body.Set(k, v)
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/"+strings.TrimLeft(path, "/"), strings.NewReader(body.Encode()))
	if e != nil {
		return "", e
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, e := c.HTTP.Do(req)
	if e != nil {
		return "", e
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", ErrTransient
	}
	b := make([]byte, 2<<20)
	n, _ := resp.Body.Read(b)
	return string(b[:n]), nil
}
