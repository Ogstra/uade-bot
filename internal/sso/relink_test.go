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
	fixtureFlowToken = "flowtoken-fixed-xyz789"
	fixtureSCtx      = "ctx-fixed-value-1"
	fixtureCanary    = "canary-fixed-abc123"
	fixtureSessionID = "session-fixed-value-1"
	fixturePassword  = "s3cr3t!"
	fixtureStartURL  = "https://inscripcionespia.uade.edu.ar/InscripcionClaseBuscar.aspx?param=abc123"

	// fixtureMRIDecoyLinkID is the MRI panel's own InscripcionAsignatura
	// link in landingHTMLMRIBeforeAsignaturas -- a value distinct from
	// fixtureStartURL and every other fixture* constant in this file, so a
	// test asserting on it could never pass "by accident".
	fixtureMRIDecoyLinkID = "https://inscripcionespia.uade.edu.ar/x?param=RELINKMRISECRET001"

	// fixtureKmsi* simulate a SECOND, distinct $Config blob served by a
	// $Config-only interstitial after the combined login POST (the KMSI
	// "Stay signed in?" hypothesis this plan generalizes
	// continueMicrosoftChain for) -- deliberately different from
	// fixtureFlowToken/fixtureSCtx/fixtureCanary/fixtureSessionID above so a
	// test asserting on these values could not pass "by accident" against
	// the first $Config's values.
	fixtureKmsiFlowToken = "flowtoken-kmsi-fixed-456"
	fixtureKmsiSCtx      = "ctx-kmsi-fixed-2"
	fixtureKmsiCanary    = "canary-kmsi-fixed-def456"
	fixtureKmsiSessionID = "session-kmsi-fixed-2"
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

// msConfigHTML serves a $Config blob synthesizing the shape confirmed live
// in 03.3-17-live-verification-notes.md ("Paso 3") -- Microsoft's raw HTML
// carries no <form>/<input> for email/password at all, only this
// JavaScript-consumed blob. urlPost is relative to the Microsoft origin,
// matching the real site's "/{tenant}/login" shape.
func msConfigHTML(urlPost, sft, sctx, canary, sessionID string) string {
	return fmt.Sprintf(`<html><body><script>
$Config={"urlPost":"%s","sFT":"%s","sCtx":"%s","canary":"%s","sessionId":"%s"};
</script></body></html>`, urlPost, sft, sctx, canary, sessionID)
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

// msLoginErrorHTML simulates Microsoft re-serving its login/error page after
// rejecting the combined password POST -- no <form> and no $Config, same
// shape as msNoFormHTML, so continueMicrosoftChain immediately stops (hop=0)
// and Relink's classification branch sees this html directly. aadstsCode is
// embedded exactly like a real Microsoft error message would (code, colon,
// human-readable text), covering both invalid-credentials codes (AADSTS50126/
// 50053/50055) and MFA codes (AADSTS50079/50076/50072/50074) -- Microsoft's
// own public, documented error-code prefixes
// (learn.microsoft.com/entra/identity-platform/reference-error-codes).
func msLoginErrorHTML(aadstsCode string) string {
	return fmt.Sprintf(`<html><body><div id="err"><p>%s: Error validando las credenciales.</p></div></body></html>`, aadstsCode)
}

// landingHTML wraps both links in the real .panel.panel-primary /
// .lbl-inscripciones panel structure extractStartURL now scopes its search
// to (03.3-22) -- the decoy link sits under a heading that does NOT start
// with "Asignaturas" (so it stays a decoy even now that panel-scoping is
// active), and the real link sits under "Asignaturas 2do Cuatrimestre
// 2026". Neither realStartURL nor either link's data-tipolink changed --
// only the panel wrapper around them.
func landingHTML(realStartURL string) string {
	return fmt.Sprintf(`<html><body>
<div class="panel panel-primary">
  <div class="panel-body">
    <div class="row list-group-item_on">
      <span class="lbl-inscripciones">Otro Módulo 2do Cuatrimestre 2026</span>
    </div>
    <a class="link-inscripciones inscribite" data-tipolink="OtroModulo" data-linkid="https://inscripcionespia.uade.edu.ar/decoy?param=zzz">Otro módulo</a>
  </div>
</div>
<div class="panel panel-primary">
  <div class="panel-body">
    <div class="row list-group-item_on">
      <span class="lbl-inscripciones">Asignaturas 2do Cuatrimestre 2026</span>
    </div>
    <a class="link-inscripciones inscribite" data-tipolink="InscripcionAsignatura" data-linkid="%s">¡INSCRIBITE!</a>
  </div>
</div>
</body></html>`, realStartURL)
}

// landingHTMLMRIBeforeAsignaturas is the byte-accurate regression fixture
// for the real production bug fixed live in Node (commit 1d56530, confirmed
// 2026-07-12, see src/automation/sso-relink-uat-checklist.md): the MRI
// panel -- sharing the EXACT SAME data-tipolink="InscripcionAsignatura" as
// the real Asignaturas panel -- is served FIRST in the DOM, followed by the
// real Asignaturas panel. Only the panel heading text distinguishes them.
func landingHTMLMRIBeforeAsignaturas(realStartURL string) string {
	return fmt.Sprintf(`<html><body>
<div class="panel panel-primary">
  <div class="panel-body">
    <div class="row list-group-item_on">
      <span class="lbl-inscripciones">Cursos Regulares Intensivos (MRI) 1er Cuatrimestre 2026</span>
    </div>
    <a class="link-inscripciones inscribite" data-tipolink="InscripcionAsignatura" data-linkid="%s">¡INSCRIBITE!</a>
  </div>
</div>
<div class="panel panel-primary">
  <div class="panel-body">
    <div class="row list-group-item_on">
      <span class="lbl-inscripciones">Asignaturas 2do Cuatrimestre 2026</span>
    </div>
    <a class="link-inscripciones inscribite" data-tipolink="InscripcionAsignatura" data-linkid="%s">¡INSCRIBITE!</a>
  </div>
</div>
</body></html>`, fixtureMRIDecoyLinkID, realStartURL)
}

// landingHTMLShellOnly simulates the real shell HTML confirmed live
// (network-log.json, spike 001): the Bootstrap tab toggle IS present in the
// raw response to GET "/", but the tab's own content (the enrollment
// panels) is not -- it only arrives via the two AJAX calls
// fetchInscripcionesPartial replicates. Matches the real symptom this file's
// debug session (.planning/debug/sso-tab-ajax-not-fetched.md) confirmed:
// bootstrapTabPresent=true, inscribeteLinkCount=0.
func landingHTMLShellOnly() string {
	return `<html><body>
<ul class="nav nav-tabs">
  <li><a data-toggle="tab" href="#menu3">Inscribite</a></li>
</ul>
<div id="menu3" class="tab-pane"></div>
</body></html>`
}

// landingHTMLNoInscribeteLinks simulates hypothesis A of 03.3-21: the final
// portal page has no a.inscribite element of any type -- the account
// genuinely has no enrollment available right now.
func landingHTMLNoInscribeteLinks() string {
	return `<html><body><p>No hay inscripciones disponibles en este momento.</p></body></html>`
}

// landingHTMLOtherTypesOnly simulates hypothesis B of 03.3-21: the final
// portal page has a.inscribite elements, but none of type
// InscripcionAsignatura -- two elements of the SAME other type (to also
// prove deduplication at the Relink level, not just extractStartURLDiagnostics
// in isolation). Each data-linkid carries a distinct secret-shaped substring
// from the one already used in start_url_diagnostics_test.go.
func landingHTMLOtherTypesOnly() string {
	return `<html><body>
<a class="inscribite" data-tipolink="CursosMRI" data-linkid="https://inscripcionespia.uade.edu.ar/x?param=RELINKSECRET001">Curso MRI 1</a>
<a class="inscribite" data-tipolink="CursosMRI" data-linkid="https://inscripcionespia.uade.edu.ar/x?param=RELINKSECRET002">Curso MRI 2</a>
</body></html>`
}

// TestRelinkFullFlow drives the complete portal -> Microsoft -> portal HTTP
// chain across both step-2 trigger hypotheses (href vs form) and both
// post-combined-login outcomes (straight back to the portal vs the "Stay
// signed in?" interstitial), asserting that the single combined POST built
// from $Config carries every field intact (email/password plus $Config's
// own flowToken/ctx/canary/sessionId).
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
				gotLogin        string
				gotLoginfmt     string
				gotPasswd       string
				gotFlowToken    string
				gotCtx          string
				gotCanary       string
				gotHpgRequestID string
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
				fmt.Fprint(w, msConfigHTML("/oauth/login", fixtureFlowToken, fixtureSCtx, fixtureCanary, fixtureSessionID))
			})
			msMux.HandleFunc("/oauth/login", func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseForm(); err != nil {
					t.Fatalf("parse combined login form: %v", err)
				}
				gotLogin = r.FormValue("login")
				gotLoginfmt = r.FormValue("loginfmt")
				gotPasswd = r.FormValue("passwd")
				gotFlowToken = r.FormValue("flowToken")
				gotCtx = r.FormValue("ctx")
				gotCanary = r.FormValue("canary")
				gotHpgRequestID = r.FormValue("hpgrequestid")
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
			if gotLogin != MicrosoftEmail("jperez") {
				t.Fatalf("gotLogin = %q, want %q", gotLogin, MicrosoftEmail("jperez"))
			}
			if gotLoginfmt != MicrosoftEmail("jperez") {
				t.Fatalf("gotLoginfmt = %q, want %q", gotLoginfmt, MicrosoftEmail("jperez"))
			}
			if gotPasswd != fixturePassword {
				t.Fatalf("gotPasswd mismatch: got %q", gotPasswd)
			}
			if gotFlowToken != fixtureFlowToken {
				t.Fatalf("gotFlowToken = %q, want %q", gotFlowToken, fixtureFlowToken)
			}
			if gotCtx != fixtureSCtx {
				t.Fatalf("gotCtx = %q, want %q", gotCtx, fixtureSCtx)
			}
			if gotCanary != fixtureCanary {
				t.Fatalf("gotCanary = %q, want %q", gotCanary, fixtureCanary)
			}
			if gotHpgRequestID != fixtureSessionID {
				t.Fatalf("gotHpgRequestID = %q, want %q", gotHpgRequestID, fixtureSessionID)
			}
		})
	}
}

// TestRelinkExtractsAsignaturasLinkNotMRIPanel is the end-to-end regression
// test for the real production bug fixed live in Node (commit 1d56530,
// confirmed 2026-07-12): it drives the SAME httptest scaffolding as
// TestRelinkFullFlow's HrefTrigger_DirectLanding case (login trigger via
// <a href>, msConfigHTML for the combined submit, direct redirect to
// /landing with no interstitial) but serves
// landingHTMLMRIBeforeAsignaturas -- the MRI panel sharing the same
// data-tipolink="InscripcionAsignatura", positioned BEFORE the Asignaturas
// panel in the DOM -- and confirms Relink() (the full HTTP flow, not just
// extractStartURL in isolation) extracts the Asignaturas panel's link and
// never the MRI panel's, despite DOM order.
func TestRelinkExtractsAsignaturasLinkNotMRIPanel(t *testing.T) {
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
	portalMux.HandleFunc("/landing", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, landingHTMLMRIBeforeAsignaturas(fixtureStartURL))
	})

	msMux.HandleFunc("/oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, msConfigHTML("/oauth/login", fixtureFlowToken, fixtureSCtx, fixtureCanary, fixtureSessionID))
	})
	msMux.HandleFunc("/oauth/login", func(w http.ResponseWriter, r *http.Request) {
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
		t.Fatalf("StartURL = %q, want %q (the Asignaturas panel's link)", result.StartURL, fixtureStartURL)
	}
	if result.StartURL == fixtureMRIDecoyLinkID {
		t.Fatalf("StartURL = %q, the MRI panel's link -- regression of the real production bug fixed in Node (commit 1d56530), despite it appearing first in the DOM", result.StartURL)
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
		fmt.Fprint(w, msConfigHTML("/oauth/login", fixtureFlowToken, fixtureSCtx, fixtureCanary, fixtureSessionID))
	})
	msMux.HandleFunc("/oauth/login", func(w http.ResponseWriter, r *http.Request) {
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

	diag, ok := MFADiagnosticsFrom(err)
	if !ok {
		t.Fatal("MFADiagnosticsFrom(err) ok = false, want true")
	}
	if diag.Hops != 0 {
		t.Fatalf("diag.Hops = %d, want 0 (msNoFormHTML has no <form>, continueMicrosoftChain returns on its first iteration)", diag.Hops)
	}
	if diag.AADSTSCode != "" {
		t.Fatalf("diag.AADSTSCode = %q, want empty string (fixture has no AADSTS code)", diag.AADSTSCode)
	}
	if diag.Host != hostnameOf(msBase) {
		t.Fatalf("diag.Host = %q, want %q", diag.Host, hostnameOf(msBase))
	}
	// No AADSTS code present at all must fail closed toward ErrMFARequired,
	// never ErrInvalidCredentials -- regression guard for the classification
	// added in .planning/debug/resolved/relink-mfa-vs-wrong-password.md.
	if errors.Is(err, ErrInvalidCredentials) {
		t.Fatal("err must NOT also be ErrInvalidCredentials when no AADSTS code is present")
	}
}

// TestRelinkClassifiesAADSTSCodeForInvalidCredentialsVsMFA is the regression
// test for .planning/debug/resolved/relink-mfa-vs-wrong-password.md: Relink
// must distinguish Microsoft's own documented invalid-credentials AADSTS
// codes (wrong/expired password, locked account -- learn.microsoft.com/entra/
// identity-platform/reference-error-codes) from real MFA/additional-
// verification codes, even though both leave the final page on a Microsoft
// host (msLoginErrorHTML, same "no <form>, no $Config" shape as
// TestRelinkMFAWhenNoFormAfterPassword's msNoFormHTML). Wrong-password-shaped
// codes must return ErrInvalidCredentials (never ErrMFARequired); MFA-shaped
// codes must keep returning ErrMFARequired (never ErrInvalidCredentials).
func TestRelinkClassifiesAADSTSCodeForInvalidCredentialsVsMFA(t *testing.T) {
	cases := []struct {
		name             string
		aadstsCode       string
		wantInvalidCreds bool
	}{
		{"WrongUsernameOrPassword", "AADSTS50126", true},
		{"AccountLocked", "AADSTS50053", true},
		{"PasswordExpired", "AADSTS50055", true},
		{"MFARequired", "AADSTS50079", false},
		{"AdditionalVerificationRequired", "AADSTS50076", false},
		{"StrongAuthRequired", "AADSTS50072", false},
		{"StrongAuthEnrollmentRequired", "AADSTS50074", false},
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

			portalMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "/Account/Login", http.StatusFound)
			})
			portalMux.HandleFunc("/Account/Login", func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, portalLoginHrefHTML(msBase+"/oauth/authorize"))
			})

			msMux.HandleFunc("/oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, msConfigHTML("/oauth/login", fixtureFlowToken, fixtureSCtx, fixtureCanary, fixtureSessionID))
			})
			msMux.HandleFunc("/oauth/login", func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, msLoginErrorHTML(tc.aadstsCode))
			})

			client, err := NewClient(portalSrv.URL)
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			result, err := Relink(context.Background(), client, portalSrv.URL, "jperez", fixturePassword)
			if !result.Manual {
				t.Fatalf("result.Manual = false, want true (result=%+v)", result)
			}

			if tc.wantInvalidCreds {
				if !errors.Is(err, ErrInvalidCredentials) {
					t.Fatalf("err = %v, want ErrInvalidCredentials", err)
				}
				if errors.Is(err, ErrMFARequired) {
					t.Fatalf("err = %v, must NOT also be ErrMFARequired", err)
				}
				diag, ok := InvalidCredentialsDiagnosticsFrom(err)
				if !ok {
					t.Fatal("InvalidCredentialsDiagnosticsFrom(err) ok = false, want true")
				}
				if diag.AADSTSCode != tc.aadstsCode {
					t.Fatalf("diag.AADSTSCode = %q, want %q", diag.AADSTSCode, tc.aadstsCode)
				}
			} else {
				if !errors.Is(err, ErrMFARequired) {
					t.Fatalf("err = %v, want ErrMFARequired", err)
				}
				if errors.Is(err, ErrInvalidCredentials) {
					t.Fatalf("err = %v, must NOT also be ErrInvalidCredentials", err)
				}
				diag, ok := MFADiagnosticsFrom(err)
				if !ok {
					t.Fatal("MFADiagnosticsFrom(err) ok = false, want true")
				}
				if diag.AADSTSCode != tc.aadstsCode {
					t.Fatalf("diag.AADSTSCode = %q, want %q", diag.AADSTSCode, tc.aadstsCode)
				}
			}
		})
	}
}

// TestRelinkAutoContinuesConfigOnlyInterstitial covers the KMSI ("Stay
// signed in?") hypothesis this plan generalizes continueMicrosoftChain for:
// after the combined login POST, Microsoft serves ANOTHER page with no
// <form> at all but a second, distinct $Config blob (simulating the
// interstitial documented in explore-sso-flow.js lines ~217-234, confirmed
// live 2026-07-12). Relink must auto-continue through it via
// continueViaMicrosoftConfig, completing successfully instead of falling
// into ErrMFARequired -- and the continuation endpoint must have received
// exactly the six fields buildMicrosoftContinuePostValues documents (four
// reused from the second $Config plus the two speculative fixed fields).
func TestRelinkAutoContinuesConfigOnlyInterstitial(t *testing.T) {
	portalMux := http.NewServeMux()
	msMux := http.NewServeMux()

	portalSrv := httptest.NewServer(portalMux)
	defer portalSrv.Close()
	msSrv := httptest.NewServer(msMux)
	defer msSrv.Close()
	msBase := localhostURL(msSrv.URL)

	setMicrosoftLoginHost(t, hostnameOf(msBase))

	var (
		gotFlowToken    string
		gotCtx          string
		gotCanary       string
		gotHpgRequestID string
		gotLoginOptions string
		gotType         string
	)

	portalMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/Account/Login", http.StatusFound)
	})
	portalMux.HandleFunc("/Account/Login", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, portalLoginHrefHTML(msBase+"/oauth/authorize"))
	})
	portalMux.HandleFunc("/landing", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, landingHTML(fixtureStartURL))
	})

	msMux.HandleFunc("/oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, msConfigHTML("/oauth/login", fixtureFlowToken, fixtureSCtx, fixtureCanary, fixtureSessionID))
	})
	msMux.HandleFunc("/oauth/login", func(w http.ResponseWriter, r *http.Request) {
		// The $Config-only interstitial: no <form> anywhere in this HTML,
		// only a second, distinct $Config blob.
		fmt.Fprint(w, msConfigHTML("/oauth/kmsi-continue", fixtureKmsiFlowToken, fixtureKmsiSCtx, fixtureKmsiCanary, fixtureKmsiSessionID))
	})
	msMux.HandleFunc("/oauth/kmsi-continue", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse kmsi-continue form: %v", err)
		}
		gotFlowToken = r.FormValue("flowToken")
		gotCtx = r.FormValue("ctx")
		gotCanary = r.FormValue("canary")
		gotHpgRequestID = r.FormValue("hpgrequestid")
		gotLoginOptions = r.FormValue("LoginOptions")
		gotType = r.FormValue("type")
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
	if gotFlowToken != fixtureKmsiFlowToken {
		t.Fatalf("gotFlowToken = %q, want %q", gotFlowToken, fixtureKmsiFlowToken)
	}
	if gotCtx != fixtureKmsiSCtx {
		t.Fatalf("gotCtx = %q, want %q", gotCtx, fixtureKmsiSCtx)
	}
	if gotCanary != fixtureKmsiCanary {
		t.Fatalf("gotCanary = %q, want %q", gotCanary, fixtureKmsiCanary)
	}
	if gotHpgRequestID != fixtureKmsiSessionID {
		t.Fatalf("gotHpgRequestID = %q, want %q", gotHpgRequestID, fixtureKmsiSessionID)
	}
	if gotLoginOptions != "1" {
		t.Fatalf("gotLoginOptions = %q, want %q", gotLoginOptions, "1")
	}
	if gotType != "28" {
		t.Fatalf("gotType = %q, want %q", gotType, "28")
	}
}

// TestRelinkStillMFAWhenSecondHopHasNoFormOrConfig proves the fail-closed
// behavior is preserved byte for byte: the FIRST hop after the combined
// login POST is the same $Config-only interstitial as
// TestRelinkAutoContinuesConfigOnlyInterstitial (and IS auto-continued), but
// the continuation endpoint this time serves a genuinely unknown page (no
// <form>, no $Config -- e.g. a real MFA prompt). Relink must still return
// ErrMFARequired, with diag.Hops == 1 confirming the first $Config hop DID
// complete before the second, truly unknown hop stopped the chain.
func TestRelinkStillMFAWhenSecondHopHasNoFormOrConfig(t *testing.T) {
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
		fmt.Fprint(w, msConfigHTML("/oauth/login", fixtureFlowToken, fixtureSCtx, fixtureCanary, fixtureSessionID))
	})
	msMux.HandleFunc("/oauth/login", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, msConfigHTML("/oauth/kmsi-continue", fixtureKmsiFlowToken, fixtureKmsiSCtx, fixtureKmsiCanary, fixtureKmsiSessionID))
	})
	msMux.HandleFunc("/oauth/kmsi-continue", func(w http.ResponseWriter, r *http.Request) {
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

	diag, ok := MFADiagnosticsFrom(err)
	if !ok {
		t.Fatal("MFADiagnosticsFrom(err) ok = false, want true")
	}
	if diag.Hops != 1 {
		t.Fatalf("diag.Hops = %d, want 1 (first hop auto-continued via $Config, second hop is genuinely unknown)", diag.Hops)
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

// TestRelinkAttachesStartURLDiagnostics drives the same full portal ->
// Microsoft -> portal HTTP chain as TestRelinkFullFlow's HrefTrigger_DirectLanding
// case, but serves a /landing page with no valid InscripcionAsignatura link,
// covering 03.3-21's two hypotheses: zero a.inscribite links at all
// (hypothesis A -- no enrollment currently available, not a bug), and
// a.inscribite links of another type only (hypothesis B -- a real selector
// problem, or the account has a different enrollment type available).
// Relink must still return an error satisfying errors.Is(err,
// ErrInvalidStartURL), now enriched with recoverable StartURLDiagnostics.
func TestRelinkAttachesStartURLDiagnostics(t *testing.T) {
	cases := []struct {
		name             string
		landingHandler   func(w http.ResponseWriter, r *http.Request)
		wantCount        int
		wantTipolinks    []string
		secretSubstrings []string
	}{
		{
			name: "ZeroLinks",
			landingHandler: func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, landingHTMLNoInscribeteLinks())
			},
			wantCount:     0,
			wantTipolinks: nil,
		},
		{
			name: "OtherTypesOnlyDeduplicated",
			landingHandler: func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, landingHTMLOtherTypesOnly())
			},
			wantCount:        2,
			wantTipolinks:    []string{"CursosMRI"},
			secretSubstrings: []string{"RELINKSECRET001", "RELINKSECRET002"},
		},
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

			portalMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "/Account/Login", http.StatusFound)
			})
			portalMux.HandleFunc("/Account/Login", func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, portalLoginHrefHTML(msBase+"/oauth/authorize"))
			})
			portalMux.HandleFunc("/landing", tc.landingHandler)

			msMux.HandleFunc("/oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, msConfigHTML("/oauth/login", fixtureFlowToken, fixtureSCtx, fixtureCanary, fixtureSessionID))
			})
			msMux.HandleFunc("/oauth/login", func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, portalSrv.URL+"/landing", http.StatusFound)
			})

			client, err := NewClient(portalSrv.URL)
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			_, err = Relink(context.Background(), client, portalSrv.URL, "jperez", fixturePassword)
			if !errors.Is(err, ErrInvalidStartURL) {
				t.Fatalf("err = %v, want ErrInvalidStartURL", err)
			}

			diag, ok := StartURLDiagnosticsFrom(err)
			if !ok {
				t.Fatal("StartURLDiagnosticsFrom(err) ok = false, want true")
			}
			if diag.InscribeteLinkCount != tc.wantCount {
				t.Fatalf("diag.InscribeteLinkCount = %d, want %d", diag.InscribeteLinkCount, tc.wantCount)
			}
			if len(diag.DataTipolinkValues) != len(tc.wantTipolinks) {
				t.Fatalf("diag.DataTipolinkValues = %v, want %v", diag.DataTipolinkValues, tc.wantTipolinks)
			}
			for i := range tc.wantTipolinks {
				if diag.DataTipolinkValues[i] != tc.wantTipolinks[i] {
					t.Fatalf("diag.DataTipolinkValues = %v, want %v", diag.DataTipolinkValues, tc.wantTipolinks)
				}
			}
			if diag.Host != hostnameOf(portalSrv.URL) {
				t.Fatalf("diag.Host = %q, want %q (final page is back on the portal, not Microsoft)", diag.Host, hostnameOf(portalSrv.URL))
			}
			// 03.3-22: neither fixture has an Asignaturas panel at all, so both
			// must report AsignaturasPanelFound == false, not just "no link".
			if diag.AsignaturasPanelFound {
				t.Fatal("diag.AsignaturasPanelFound = true, want false (neither fixture has an Asignaturas panel)")
			}

			serialized := fmt.Sprintf("%+v", diag)
			for _, secret := range tc.secretSubstrings {
				if strings.Contains(serialized, secret) {
					t.Fatalf("diagnostics leaked data-linkid secret substring: %q contains %q", serialized, secret)
				}
			}
		})
	}
}

// TestRelinkFetchesInscripcionesPartialWhenShellIsEmpty is the regression
// test for the root cause documented in
// .planning/debug/resolved/sso-tab-ajax-not-fetched.md: the final portal
// page's raw HTML (/landing) is only the shell (Bootstrap tab present, no
// enrollment panels -- landingHTMLShellOnly), and the real panel content
// only exists behind the two AJAX GETs a real browser fires unconditionally
// on page load (GET /Home/ValidarUsuario -> GET /Home/ObtenerInscripciones,
// confirmed live 2026-07-12 via network-log.json). Relink must still
// succeed by fetching and merging that content, exactly reproducing the
// live symptom (bootstrapTabPresent=true, inscribeteLinkCount=0 from the
// shell alone) and its fix.
func TestRelinkFetchesInscripcionesPartialWhenShellIsEmpty(t *testing.T) {
	portalMux := http.NewServeMux()
	msMux := http.NewServeMux()

	portalSrv := httptest.NewServer(portalMux)
	defer portalSrv.Close()
	msSrv := httptest.NewServer(msMux)
	defer msSrv.Close()
	msBase := localhostURL(msSrv.URL)

	setMicrosoftLoginHost(t, hostnameOf(msBase))

	var (
		gotValidarAjaxHeader string
		gotObtenerAjaxHeader string
		obtenerInscripciones int
	)

	portalMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/Account/Login", http.StatusFound)
	})
	portalMux.HandleFunc("/Account/Login", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, portalLoginHrefHTML(msBase+"/oauth/authorize"))
	})
	portalMux.HandleFunc("/landing", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, landingHTMLShellOnly())
	})
	portalMux.HandleFunc("/Home/ValidarUsuario", func(w http.ResponseWriter, r *http.Request) {
		gotValidarAjaxHeader = r.Header.Get("X-Requested-With")
		fmt.Fprint(w, `{"ok":true}`)
	})
	portalMux.HandleFunc("/Home/ObtenerInscripciones", func(w http.ResponseWriter, r *http.Request) {
		obtenerInscripciones++
		gotObtenerAjaxHeader = r.Header.Get("X-Requested-With")
		fmt.Fprint(w, landingHTML(fixtureStartURL))
	})

	msMux.HandleFunc("/oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, msConfigHTML("/oauth/login", fixtureFlowToken, fixtureSCtx, fixtureCanary, fixtureSessionID))
	})
	msMux.HandleFunc("/oauth/login", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, portalSrv.URL+"/landing", http.StatusFound)
	})

	client, err := NewClient(portalSrv.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	result, err := Relink(context.Background(), client, portalSrv.URL, "jperez", fixturePassword)
	if err != nil {
		t.Fatalf("Relink error: %v (shell-only landing page should have been recovered via the AJAX partial)", err)
	}
	if result.StartURL != fixtureStartURL {
		t.Fatalf("StartURL = %q, want %q", result.StartURL, fixtureStartURL)
	}
	if obtenerInscripciones != 1 {
		t.Fatalf("ObtenerInscripciones called %d times, want 1", obtenerInscripciones)
	}
	if gotValidarAjaxHeader != "XMLHttpRequest" {
		t.Fatalf("ValidarUsuario X-Requested-With = %q, want %q", gotValidarAjaxHeader, "XMLHttpRequest")
	}
	if gotObtenerAjaxHeader != "XMLHttpRequest" {
		t.Fatalf("ObtenerInscripciones X-Requested-With = %q, want %q", gotObtenerAjaxHeader, "XMLHttpRequest")
	}
}

// TestRelinkSkipsObtenerInscripcionesWhenValidarUsuarioFails proves the
// sequencing dependency documented in fetchInscripcionesPartial: when
// ValidarUsuario errors, ObtenerInscripciones must never be called (mirrors
// the real page's own JS only firing the second AJAX call from the first
// call's success handler), and Relink must fall back to the shell HTML
// alone -- failing closed with ErrInvalidStartURL exactly as it did before
// this fix existed, not panicking or hanging.
func TestRelinkSkipsObtenerInscripcionesWhenValidarUsuarioFails(t *testing.T) {
	portalMux := http.NewServeMux()
	msMux := http.NewServeMux()

	portalSrv := httptest.NewServer(portalMux)
	defer portalSrv.Close()
	msSrv := httptest.NewServer(msMux)
	defer msSrv.Close()
	msBase := localhostURL(msSrv.URL)

	setMicrosoftLoginHost(t, hostnameOf(msBase))

	var obtenerInscripcionesHit bool

	portalMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/Account/Login", http.StatusFound)
	})
	portalMux.HandleFunc("/Account/Login", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, portalLoginHrefHTML(msBase+"/oauth/authorize"))
	})
	portalMux.HandleFunc("/landing", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, landingHTMLShellOnly())
	})
	portalMux.HandleFunc("/Home/ValidarUsuario", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	portalMux.HandleFunc("/Home/ObtenerInscripciones", func(w http.ResponseWriter, r *http.Request) {
		obtenerInscripcionesHit = true
		fmt.Fprint(w, landingHTML(fixtureStartURL))
	})

	msMux.HandleFunc("/oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, msConfigHTML("/oauth/login", fixtureFlowToken, fixtureSCtx, fixtureCanary, fixtureSessionID))
	})
	msMux.HandleFunc("/oauth/login", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, portalSrv.URL+"/landing", http.StatusFound)
	})

	client, err := NewClient(portalSrv.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = Relink(context.Background(), client, portalSrv.URL, "jperez", fixturePassword)
	if !errors.Is(err, ErrInvalidStartURL) {
		t.Fatalf("err = %v, want ErrInvalidStartURL", err)
	}
	if obtenerInscripcionesHit {
		t.Fatal("ObtenerInscripciones was called despite ValidarUsuario failing -- must preserve the real sequencing dependency")
	}
}

// TestRelinkMergesAjaxPartialDiagnosticsWithShell proves the merge (not
// replace) behavior: BootstrapTabPresent must still be reported from the
// shell HTML even when the AJAX partial is what supplies the a.inscribite
// links -- losing that diagnostic signal would make future ErrInvalidStartURL
// investigations harder to distinguish from a genuinely different failure
// mode.
func TestRelinkMergesAjaxPartialDiagnosticsWithShell(t *testing.T) {
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
	portalMux.HandleFunc("/landing", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, landingHTMLShellOnly())
	})
	portalMux.HandleFunc("/Home/ValidarUsuario", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"ok":true}`)
	})
	portalMux.HandleFunc("/Home/ObtenerInscripciones", func(w http.ResponseWriter, r *http.Request) {
		// Other-type-only content -- no InscripcionAsignatura link, so Relink
		// still fails closed, but the diagnostics attached to that failure
		// must show the merged picture: BootstrapTabPresent from the shell,
		// InscribeteLinkCount>0 from the AJAX partial.
		fmt.Fprint(w, landingHTMLOtherTypesOnly())
	})

	msMux.HandleFunc("/oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, msConfigHTML("/oauth/login", fixtureFlowToken, fixtureSCtx, fixtureCanary, fixtureSessionID))
	})
	msMux.HandleFunc("/oauth/login", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, portalSrv.URL+"/landing", http.StatusFound)
	})

	client, err := NewClient(portalSrv.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = Relink(context.Background(), client, portalSrv.URL, "jperez", fixturePassword)
	if !errors.Is(err, ErrInvalidStartURL) {
		t.Fatalf("err = %v, want ErrInvalidStartURL", err)
	}

	diag, ok := StartURLDiagnosticsFrom(err)
	if !ok {
		t.Fatal("StartURLDiagnosticsFrom(err) ok = false, want true")
	}
	if !diag.BootstrapTabPresent {
		t.Fatal("diag.BootstrapTabPresent = false, want true (present in the shell HTML, must survive the merge with the AJAX partial)")
	}
	if diag.InscribeteLinkCount != 2 {
		t.Fatalf("diag.InscribeteLinkCount = %d, want 2 (from the AJAX partial, landingHTMLOtherTypesOnly)", diag.InscribeteLinkCount)
	}
}

// TestExtractStartURLMissingLinkFailsClosed is a direct unit test of
// extractStartURL against HTML with no matching InscripcionAsignatura link.
func TestExtractStartURLMissingLinkFailsClosed(t *testing.T) {
	if _, err := extractStartURL(`<html><body>no link here</body></html>`); !errors.Is(err, ErrInvalidStartURL) {
		t.Fatalf("err = %v, want ErrInvalidStartURL", err)
	}
}

// TestBuildMicrosoftContinuePostValues confirms the exact six-key mapping
// buildMicrosoftContinuePostValues produces from a microsoftConfig fixture --
// the four reused $Config values plus the two speculative fixed KMSI fields
// documented in relink.go, and nothing else (in particular never a urlPost
// form field -- the POST target is resolved separately, not submitted as a
// form value).
func TestBuildMicrosoftContinuePostValues(t *testing.T) {
	cfg := microsoftConfig{
		URLPost:   "/oauth/kmsi-continue",
		SFT:       fixtureFlowToken,
		SCtx:      fixtureSCtx,
		Canary:    fixtureCanary,
		SessionID: fixtureSessionID,
	}
	values := buildMicrosoftContinuePostValues(cfg)

	if len(values) != 6 {
		t.Fatalf("len(values) = %d, want 6 (values=%v)", len(values), values)
	}
	if got := values.Get("flowToken"); got != fixtureFlowToken {
		t.Fatalf("flowToken = %q, want %q", got, fixtureFlowToken)
	}
	if got := values.Get("ctx"); got != fixtureSCtx {
		t.Fatalf("ctx = %q, want %q", got, fixtureSCtx)
	}
	if got := values.Get("canary"); got != fixtureCanary {
		t.Fatalf("canary = %q, want %q", got, fixtureCanary)
	}
	if got := values.Get("hpgrequestid"); got != fixtureSessionID {
		t.Fatalf("hpgrequestid = %q, want %q", got, fixtureSessionID)
	}
	if got := values.Get("LoginOptions"); got != "1" {
		t.Fatalf("LoginOptions = %q, want %q", got, "1")
	}
	if got := values.Get("type"); got != "28" {
		t.Fatalf("type = %q, want %q", got, "28")
	}
	if values.Has("urlPost") {
		t.Fatal("buildMicrosoftContinuePostValues must never submit urlPost as a form field")
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
