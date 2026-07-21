package dashboard

import (
	"net/http/httptest"
	"testing"
)

func TestServerAuthAndHealth(t *testing.T) {
	s := Server{User: "a", Password: "b"}
	r := httptest.NewRequest("GET", "/api/health", nil)
	r.SetBasicAuth("a", "b")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("%d", w.Code)
	}
}
