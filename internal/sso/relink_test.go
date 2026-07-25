package sso

// The HTML fixtures in this file are synthetic markup inspired by the
// structure explore-sso-flow.js confirmed live against the real UADE/
// Microsoft sites (selectors, step order) -- they are NOT captures of the
// real site. Step 2 in particular (the portal's own "Iniciar sesión"
// trigger) is the one piece explore-sso-flow.js only confirmed via
// Playwright role locators (getByRole('button'...).or(getByRole('link'...))),
// not raw HTTP -- these fixtures cover our two best hypotheses for its HTTP
// shape (a navigable <a href>, and a <button>/<form> self-post). Real
// verification against UADE and Microsoft live is the checkpoint in
// 03.3-17-PLAN.md.
//
// Every subtest below that needs to simulate "we're on a Microsoft page"
// runs a SECOND httptest server addressed via the "localhost" hostname
// (while the portal server keeps its natural "127.0.0.1" hostname) and
// temporarily repoints the package-level microsoftLoginHost var at
// "localhost". Two distinct hostnames are required because every host
// comparison in this package (allowedHosts, IsMicrosoftLogin,
// CheckRedirect) matches on Hostname() only, ignoring port -- two
// httptest.NewServer instances both default to 127.0.0.1 on different
// ports, which would be indistinguishable to that comparison.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

const (
	fixtureLoginCanary    = "canary-fixed-abc123"
	fixturePasswordCanary = "flowtoken-fixed-xyz789"
	fixturePassword       = "s3cr3t!"
	fixtureStartURL       = "https://inscripcionespia.uade.edu.ar/InscripcionClaseBuscar.aspx?param=abc123"
)

func localhostURL(rawURL string) string {
	return strings.Replace(rawURL, "127.0.0.1", "localhost", 1)
}

func hostnameOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func setMicrosoftLoginHost(t *testing.T, host string) {
	t.Helper()
	previous := microsoftLoginHost
	microsoftLoginHost = host
	t.Cleanup(func() { microsoftLoginHost = previous })
}

func portalLoginHrefHTML(triggerURL string) string {
	return fmt.Sprintf(`<html><body><a href="%s">Iniciar sesión</a></body></html>`, triggerURL)
}

func portalLoginFormHTML(triggerURL string) string {
	return fmt.Sprintf(`<html><body>
<form action="%s" method="post">
  <button id="x">Iniciar sesión</button>
</form>
</body></html>`, triggerURL)
}

func msStep1HTML(actionURL string) string {
	return fmt.Sprintf(`<html><body>
<form action="%s" method="post">
  <input type="hidden" name="canary" value="%s">
  <input type="hidden" name="ctx" value="ctx-value-1">
  <input type="email" name="loginfmt" id="i0116" value="">
  <input type="submit" id="idSIButton9" value="Siguiente">
</form>
</body></html>`, actionURL, fixtureLoginCanary)
}

func msStep2HTML(actionURL string) string {
	return fmt.Sprintf(`<html><body>
<form action="%s" method="post">
  <input type="hidden" name="flowtoken" value="%s">
  <input type="password" name="passwd" id="i0118" value="">
  <input type="submit" id="idSIButton9" value="Iniciar sesión">
</form>
</body></html>`, actionURL, fixturePasswordCanary)
}

func msInterstitialHTML(actionURL string) string {
	return fmt.Sprintf(`<html><body>
<form action="%s" method="post">
  <input type="hidden" name="stay" value="stay-fixed-value">
  <input type="submit" id="idSIButton9" value="Yes">
</form>
</body></html>`, actionURL)
}

func msNoFormHTML() string {
	return `<html><body><p>Ingresá el código de verificación enviado a tu teléfono.</p></body></html>`
}

func landingHTML(realStartURL string) string {
	return fmt.Sprintf(`<html><body>
<a class="link-inscripciones inscribite" data-tipolink="OtroModulo" data-linkid="https://inscripcionespia.uade.edu.ar/decoy?param=zzz">Otro módulo</a>
<a class="link-inscripciones inscribite" data-tipolink="InscripcionAsignatura" data-linkid="%s">¡INSCRIBITE!</a>
</body></html>`, realStartURL)
}

// TestRelinkFullFlow drives the complete portal -> Microsoft -> portal HTTP
// chain across both step-2 trigger hypotheses (href vs form) and both
// post-password outcomes (straight back to the portal vs the "Stay signed
// in?" interstitial), asserting that every hidden field the Microsoft forms
// serve gets forwarded intact on the following POST.
func TestRelinkFullFlow(t *testing.T) {
	cases := []struct {
		name         string
		formTrigger  bool
		interstitial bool
	}{
		{"HrefTrigger_DirectLanding", false, false},
		{"FormTrigger_DirectLanding", true, false},
		{"HrefTrigger_Interstitial", false, true},
		{"FormTrigger_Interstitial", true, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			portalMux := http.NewServeMux()
			msMux := http.NewServeMux()

			portalSrv := httptest.NewServer(portalMux)
			defer portalSrv.Close()
			msSrv := httptest.NewServer(msMux)
			defer msSrv.Close()
			msBase := localhostURL(msSrv.URL)

			setMicrosoftLoginHost(t, hostnameOf(msBase))

			var (
				emailStepCanary    string
				gotEmail           string
				passwordStepCanary string
				gotPassword        string
			)

			portalMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "/Account/Login", http.StatusFound)
			})
			portalMux.HandleFunc("/Account/Login", func(w http.ResponseWriter, r *http.Request) {
				trigger := msBase + "/oauth/authorize"
				if tc.formTrigger {
					fmt.Fprint(w, portalLoginFormHTML(trigger))
				} else {
					fmt.Fprint(w, portalLoginHrefHTML(trigger))
				}
			})
			portalMux.HandleFunc("/landing", func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, landingHTML(fixtureStartURL))
			})

			msMux.HandleFunc("/oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, msStep1HTML("/oauth/step2"))
			})
			msMux.HandleFunc("/oauth/step2", func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseForm(); err != nil {
					t.Fatalf("parse email-step form: %v", err)
				}
				emailStepCanary = r.FormValue("canary")
				gotEmail = r.FormValue("loginfmt")
				fmt.Fprint(w, msStep2HTML("/oauth/step3"))
			})
			msMux.HandleFunc("/oauth/step3", func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseForm(); err != nil {
					t.Fatalf("parse password-step form: %v", err)
				}
				passwordStepCanary = r.FormValue("flowtoken")
				gotPassword = r.FormValue("passwd")
				if tc.interstitial {
					fmt.Fprint(w, msInterstitialHTML("/oauth/interstitial-continue"))
					return
				}
				http.Redirect(w, r, portalSrv.URL+"/landing", http.StatusFound)
			})
			msMux.HandleFunc("/oauth/interstitial-continue", func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, portalSrv.URL+"/landing", http.StatusFound)
			})

			client, err := NewClient(portalSrv.URL)
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			result, err := Relink(context.Background(), client, portalSrv.URL, "jperez", fixturePassword)
			if err != nil {
				t.Fatalf("Relink error: %v", err)
			}
			if result.Manual {
				t.Fatalf("unexpected Manual=true result=%+v", result)
			}
			if result.StartURL != fixtureStartURL {
				t.Fatalf("StartURL = %q, want %q", result.StartURL, fixtureStartURL)
			}
			if emailStepCanary != fixtureLoginCanary {
				t.Fatalf("email-step hidden canary not forwarded intact: got %q want %q", emailStepCanary, fixtureLoginCanary)
			}
			if gotEmail != MicrosoftEmail("jperez") {
				t.Fatalf("gotEmail = %q, want %q", gotEmail, MicrosoftEmail("jperez"))
			}
			if passwordStepCanary != fixturePasswordCanary {
				t.Fatalf("password-step hidden flowtoken not forwarded intact: got %q want %q", passwordStepCanary, fixturePasswordCanary)
			}
			if gotPassword != fixturePassword {
				t.Fatalf("gotPassword mismatch: got %q", gotPassword)
			}
		})
	}
}

// TestRelinkMFAWhenNoFormAfterPassword covers the MFA/additional-verification
// path: after the password submit, Microsoft keeps serving a page with no
// <form> at all (simulating a real verification-code prompt). Relink must
// stop there and report ErrMFARequired without ever trying to invent or
// submit a value it wasn't given.
func TestRelinkMFAWhenNoFormAfterPassword(t *testing.T) {
	portalMux := http.NewServeMux()
	msMux := http.NewServeMux()

	portalSrv := httptest.NewServer(portalMux)
	defer portalSrv.Close()
	msSrv := httptest.NewServer(msMux)
	defer msSrv.Close()
	msBase := localhostURL(msSrv.URL)

	setMicrosoftLoginHost(t, hostnameOf(msBase))

	portalMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/Account/Login", http.StatusFound)
	})
	portalMux.HandleFunc("/Account/Login", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, portalLoginHrefHTML(msBase+"/oauth/authorize"))
	})

	msMux.HandleFunc("/oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, msStep1HTML("/oauth/step2"))
	})
	msMux.HandleFunc("/oauth/step2", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, msStep2HTML("/oauth/step3"))
	})
	msMux.HandleFunc("/oauth/step3", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, msNoFormHTML())
	})

	client, err := NewClient(portalSrv.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	result, err := Relink(context.Background(), client, portalSrv.URL, "jperez", fixturePassword)
	if !errors.Is(err, ErrMFARequired) {
		t.Fatalf("err = %v, want ErrMFARequired", err)
	}
	if !result.Manual {
		t.Fatalf("result.Manual = false, want true (result=%+v)", result)
	}
}

// TestRelinkRejectsUnverifiedLoginTriggerHost proves the allowlist is
// enforced fail-closed even for a target extracted from parsed HTML (not
// just server-issued redirects): the login trigger points at a host outside
// {portal, microsoftLoginHost}, and that host must never receive a request.
func TestRelinkRejectsUnverifiedLoginTriggerHost(t *testing.T) {
	evilHit := false
	evilSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		evilHit = true
	}))
	defer evilSrv.Close()

	portalMux := http.NewServeMux()
	portalSrv := httptest.NewServer(portalMux)
	defer portalSrv.Close()

	portalMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/Account/Login", http.StatusFound)
	})
	// evilSrv must resolve to a hostname distinct from portalSrv's -- both
	// default to "127.0.0.1" on different ports otherwise, which
	// allowedHosts (a Hostname()-only comparison) would treat as the same,
	// allowed host.
	evilBase := localhostURL(evilSrv.URL)
	portalMux.HandleFunc("/Account/Login", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, portalLoginHrefHTML(evilBase+"/phish"))
	})

	client, err := NewClient(portalSrv.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := Relink(context.Background(), client, portalSrv.URL, "jperez", fixturePassword); err == nil {
		t.Fatal("expected error for a login trigger pointing outside the allowlist")
	}
	if evilHit {
		t.Fatal("the disallowed host received a request")
	}
}

// TestExtractStartURLMissingLinkFailsClosed is a direct unit test of
// extractStartURL against HTML with no matching InscripcionAsignatura link.
func TestExtractStartURLMissingLinkFailsClosed(t *testing.T) {
	if _, err := extractStartURL(`<html><body>no link here</body></html>`); !errors.Is(err, ErrInvalidStartURL) {
		t.Fatalf("err = %v, want ErrInvalidStartURL", err)
	}
}

func TestSSOURLHelpersFailClosed(t *testing.T) {
	if MicrosoftEmail("jperez") != "jperez@uade.edu.ar" || MicrosoftEmail("jperez@example.com") != "jperez@example.com" {
		t.Fatal("Microsoft email normalization mismatch")
	}
	if !IsMicrosoftLogin("https://login.microsoftonline.com/tenant") || IsMicrosoftLogin("https://login.microsoftonline.com.evil.example/") {
		t.Fatal("Microsoft host validation mismatch")
	}
	if !ValidStartURL("https://inscripcionespia.uade.edu.ar/x?param=abc") {
		t.Fatal("valid enrollment URL rejected")
	}
	for _, candidate := range []string{
		"http://inscripcionespia.uade.edu.ar/x?param=abc",
		"https://inscripcionespia.uade.edu.ar.evil.example/x?param=abc",
		"https://inscripcionespia.uade.edu.ar/x?other=abc",
	} {
		if ValidStartURL(candidate) {
			t.Fatalf("accepted %q", candidate)
		}
	}
}
