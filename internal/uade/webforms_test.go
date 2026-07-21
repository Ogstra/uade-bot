package uade

import (
	"context"
	"net/http"
	"net/http/httptest"
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
