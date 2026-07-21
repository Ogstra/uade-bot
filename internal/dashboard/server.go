package dashboard

import (
	"html/template"
	"net/http"
)

type Server struct {
	User, Password string
	Template       *template.Template
}

func (s Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	u, p, ok := r.BasicAuth()
	if !ok || u != s.User || p != s.Password {
		w.Header().Set("WWW-Authenticate", `Basic realm="uade"`)
		http.Error(w, "unauthorized", 401)
		return
	}
	if r.URL.Path == "/api/health" {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if s.Template != nil {
		_ = s.Template.Execute(w, map[string]string{"Status": "ok"})
	} else {
		_, _ = w.Write([]byte("<html><body>UADE bot</body></html>"))
	}
}
