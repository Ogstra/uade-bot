package app

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	credentialcrypto "github.com/Ogstra/uade-bot/internal/crypto"
	"github.com/Ogstra/uade-bot/internal/scheduler"
	"github.com/Ogstra/uade-bot/internal/sso"
)

// validStartURL satisfies sso.ValidStartURL, so the poll path uses it as-is
// instead of forcing a relink -- the state a user is in during a closed
// period, when their saved link is fine and the page behind it is not.
const validStartURL = "https://inscripcionespia.uade.edu.ar/InscripcionClaseBuscar.aspx?param=saved"

// The whole point of the closed-enrollment path: a relink that fails because
// UADE is between enrollment periods must NOT leave the account on
// needs_new_start_url, which is the state that drives the "run /credenciales
// to relink" DM. Nothing the user does can conjure an enrollment link that
// UADE is not publishing, so asking them to try is a loop with no exit.
func TestPollReportsClosedEnrollmentInsteadOfBlamingTheStartURL(t *testing.T) {
	db := newTestRuntimeDB(t)
	const account = "user-closed-period"
	seedRuntimeAccount(t, db, account, "u", "p", "https://inscripcionespia.uade.edu.ar/InscripcionClaseBuscar.aspx")
	jobID := seedRuntimeJob(t, db, account, `{"materiaCodigo":"3.1.050","ofrecimiento":"curricular","turno":"mañana","dias":["LU"]}`)

	runtime, err := NewRuntime(context.Background(), db, testMasterKey, "", "https://inscripciones.uade.edu.ar/", time.Minute, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	runtime.relink = func(context.Context, *http.Client, string, string, string) (sso.Result, error) {
		return sso.Result{}, fmt.Errorf("relink: %w", sso.ErrEnrollmentClosed)
	}

	outcome, err := runtime.poll(context.Background(), jobID, account)
	if err != nil {
		t.Fatalf("poll returned unexpected error: %v", err)
	}
	if outcome.Code != "inscripciones_cerradas" {
		t.Fatalf("outcome.Code = %q, want inscripciones_cerradas", outcome.Code)
	}

	var reason sql.NullString
	if err = db.QueryRow(`SELECT pause_reason FROM users WHERE discord_user_id=?`, account).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if reason.String == "needs_new_start_url" || reason.String == "needs_credentials" {
		t.Fatalf("pause_reason = %q: a closed enrollment period must never be recorded as something the user has to fix", reason.String)
	}
}

// A relink that fails for any other reason keeps the existing behavior, so a
// genuinely stale link still tells the user to act. Guards against the new
// branch swallowing real failures into a reassuring "closed" state.
func TestPollStillReportsStaleStartURLForOtherRelinkFailures(t *testing.T) {
	db := newTestRuntimeDB(t)
	const account = "user-real-stale"
	seedRuntimeAccount(t, db, account, "u", "p", "https://inscripcionespia.uade.edu.ar/InscripcionClaseBuscar.aspx")
	jobID := seedRuntimeJob(t, db, account, `{"materiaCodigo":"3.1.050","ofrecimiento":"curricular","turno":"mañana","dias":["LU"]}`)

	runtime, err := NewRuntime(context.Background(), db, testMasterKey, "", "https://inscripciones.uade.edu.ar/", time.Minute, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	runtime.relink = func(context.Context, *http.Client, string, string, string) (sso.Result, error) {
		return sso.Result{Manual: true}, sso.ErrMFARequired
	}

	outcome, err := runtime.poll(context.Background(), jobID, account)
	if err != nil {
		t.Fatalf("poll returned unexpected error: %v", err)
	}
	if outcome.Code != "stale_start_url" {
		t.Fatalf("outcome.Code = %q, want stale_start_url", outcome.Code)
	}
}

// The common production shape of a closed period: the stored start URL is
// still well-formed, so nothing forces a relink, and the search itself comes
// back empty-handed because the enrollment page is simply not published.
// uade.Search cannot tell that apart from a dead link, so the runtime asks
// the portal directly before deciding whose fault it is.
func TestStaleSearchResultIsReclassifiedWhenThePortalSaysClosed(t *testing.T) {
	db := newTestRuntimeDB(t)
	const account = "user-valid-link-dead-page"
	seedRuntimeAccount(t, db, account, "u", "p", validStartURL)

	runtime, err := NewRuntime(context.Background(), db, testMasterKey, "", "https://inscripciones.uade.edu.ar/", time.Minute, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	runtime.relink = func(context.Context, *http.Client, string, string, string) (sso.Result, error) {
		return sso.Result{}, fmt.Errorf("relink: %w", sso.ErrEnrollmentClosed)
	}

	got := runtime.resolveStaleOutcome(context.Background(), account, credentialcrypto.Credentials{UADEUsername: "u", UADEPassword: "p", UADEStartURL: validStartURL})

	if got != "inscripciones_cerradas" {
		t.Fatalf("outcome = %q, want inscripciones_cerradas", got)
	}
}

// A link that really is stale still reports as stale, so the user still gets
// told to relink when relinking is actually the fix.
func TestStaleSearchResultStaysStaleWhenRelinkCannotHelp(t *testing.T) {
	db := newTestRuntimeDB(t)
	const account = "user-genuinely-stale"
	seedRuntimeAccount(t, db, account, "u", "p", validStartURL)

	runtime, err := NewRuntime(context.Background(), db, testMasterKey, "", "https://inscripciones.uade.edu.ar/", time.Minute, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	runtime.relink = func(context.Context, *http.Client, string, string, string) (sso.Result, error) {
		return sso.Result{Manual: true}, sso.ErrMFARequired
	}

	got := runtime.resolveStaleOutcome(context.Background(), account, credentialcrypto.Credentials{UADEUsername: "u", UADEPassword: "p", UADEStartURL: validStartURL})

	if got != "stale_start_url" {
		t.Fatalf("outcome = %q, want stale_start_url", got)
	}
}

// If the probe produces a fresh link, this cycle simply failed and the next
// one will use the new URL. Reporting stale here would pause the account and
// DM the user about a link that was just repaired.
func TestStaleSearchResultDowngradesToTransientWhenRelinkSucceeds(t *testing.T) {
	db := newTestRuntimeDB(t)
	const account = "user-healed"
	seedRuntimeAccount(t, db, account, "u", "p", validStartURL)

	runtime, err := NewRuntime(context.Background(), db, testMasterKey, "", "https://inscripciones.uade.edu.ar/", time.Minute, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	const healed = "https://inscripcionespia.uade.edu.ar/InscripcionClaseBuscar.aspx?param=fresh"
	runtime.relink = func(context.Context, *http.Client, string, string, string) (sso.Result, error) {
		return sso.Result{StartURL: healed}, nil
	}

	got := runtime.resolveStaleOutcome(context.Background(), account, credentialcrypto.Credentials{UADEUsername: "u", UADEPassword: "p", UADEStartURL: validStartURL})

	if got != "search_failed" {
		t.Fatalf("outcome = %q, want search_failed (no pause, retried next cycle)", got)
	}
	var reason sql.NullString
	if err = db.QueryRow(`SELECT pause_reason FROM users WHERE discord_user_id=?`, account).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if reason.String != "" {
		t.Fatalf("pause_reason = %q, want empty after a successful repair", reason.String)
	}
}

// The DM copy has to say the searches are intact and will resume on their
// own, because the failure mode this replaces is a user believing their
// searches silently died.
func TestClosedEnrollmentNotificationTellsUserNothingIsRequired(t *testing.T) {
	text := notificationText(scheduler.Event{Kind: "account_pause", Reason: "inscripciones_cerradas"})

	if !strings.Contains(strings.ToLower(text), "inscripciones") {
		t.Fatalf("closed-enrollment DM does not name the cause: %s", text)
	}
	if !strings.Contains(strings.ToLower(text), "avis") {
		t.Fatalf("closed-enrollment DM does not promise to come back: %s", text)
	}
	for _, forbidden := range []string{"/credenciales", "revincul"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("closed-enrollment DM asks the user to act (%q): %s", forbidden, text)
		}
	}
}

func TestResumedNotificationAnnouncesSearchesAreLiveAgain(t *testing.T) {
	text := notificationText(scheduler.Event{Kind: "account_resumed", Reason: "inscripciones_cerradas"})

	if !strings.Contains(strings.ToLower(text), "inscripciones") {
		t.Fatalf("reopening message does not name what changed: %s", text)
	}
	if strings.Contains(text, "Pausé") {
		t.Fatalf("reopening message reads as a pause: %s", text)
	}
	// It must not be mistaken for a vacancy alert, which is the only other
	// unsolicited DM this bot sends.
	if strings.Contains(strings.ToLower(text), "vacante") {
		t.Fatalf("reopening message looks like a vacancy alert: %s", text)
	}
}
