package app

import (
	"bytes"
	"context"
	"crypto/tls"
	"database/sql"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	credentialcrypto "github.com/ogs/uade-bot/internal/crypto"
	"github.com/ogs/uade-bot/internal/sso"
	"github.com/ogs/uade-bot/internal/store"
)

// TestRecoverGoroutineConvertsPanicToLogInsteadOfCrashing proves recoverGoroutine
// contains a panic to the goroutine it runs in instead of crashing the process
// (CR-01 in 03.3-REVIEW.md), AND that it never leaks the arbitrary panic value
// into the log (T-03.3-13-01 / GO-02 / D-06). Start/JobCreated/AccountReady/
// JobsChanged all defer recoverGoroutine as their first statement (safeTick for
// Start's ticker case), so this isolated goroutine reproduces the same recover
// pattern they rely on, with a sentinel that simulates a leaked UADE password,
// Discord token, and start-URL query param.
//
// Not run with t.Parallel: the standard logger is global process state, and
// this test captures/restores it.
func TestRecoverGoroutineConvertsPanicToLogInsteadOfCrashing(t *testing.T) {
	origWriter := log.Writer()
	origFlags := log.Flags()
	origPrefix := log.Prefix()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	log.SetFlags(0)
	log.SetPrefix("")
	defer func() {
		log.SetOutput(origWriter)
		log.SetFlags(origFlags)
		log.SetPrefix(origPrefix)
	}()

	const (
		sensitivePassword = "hunter2-uade-password"
		sensitiveToken    = "discord-token-abc123"
		sensitiveURLParam = "param=eyJhbGciOiJI"
	)
	sentinel := sensitivePassword + " " + sensitiveToken + " https://inscripcionespia.uade.edu.ar/x?" + sensitiveURLParam

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer recoverGoroutine("test")
		panic(sentinel)
	}()

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("goroutine did not return within 1s; panic was not recovered")
	}

	logged := buf.String()

	prohibited := []string{sentinel, sensitivePassword, sensitiveToken, sensitiveURLParam}
	for _, fragment := range prohibited {
		if strings.Contains(logged, fragment) {
			t.Fatal("runtime recovery log contains prohibited sensitive content")
		}
	}

	const allowedMarker = "runtime goroutine panic recovered stage=test"
	if !strings.Contains(logged, allowedMarker) {
		t.Fatalf("recovery log missing expected marker %q", allowedMarker)
	}
}

// testMasterKey mirrors the fixed 32-byte-hex master key used by other
// packages' tests (e.g. internal/discordhttp/commands_test.go) -- never a
// real secret.
const testMasterKey = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

func newTestRuntimeDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// seedRuntimeAccount inserts a users row and a matching encrypted
// credentials row. Two separate Exec calls on purpose, mirroring
// internal/discordhttp/commands_test.go's seedCredentials: modernc.org/sqlite
// does not reliably split placeholders across statements packed into a
// single Exec.
func seedRuntimeAccount(t *testing.T, db *sql.DB, account, username, password, startURL string) {
	t.Helper()
	encrypted, err := credentialcrypto.Encrypt(testMasterKey, account, credentialcrypto.Credentials{UADEUsername: username, UADEPassword: password, UADEStartURL: startURL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO users(discord_user_id,created_at,updated_at) VALUES(?,1,1)`, account); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO credentials(discord_user_id,ciphertext,iv,auth_tag,updated_at) VALUES(?,?,?,?,1)`, account, encrypted.Ciphertext, encrypted.IV, encrypted.AuthTag); err != nil {
		t.Fatal(err)
	}
}

func seedRuntimeJob(t *testing.T, db *sql.DB, account, filtrosJSON string) string {
	t.Helper()
	res, err := db.Exec(`INSERT INTO jobs(discord_user_id,filtros_json,status,created_at) VALUES(?,?,'active',1)`, account, filtrosJSON)
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return strconv.FormatInt(id, 10)
}

func decryptRuntimeCredentials(t *testing.T, db *sql.DB, account string) credentialcrypto.Credentials {
	t.Helper()
	var encrypted credentialcrypto.Ciphertext
	if err := db.QueryRow(`SELECT ciphertext, iv, auth_tag FROM credentials WHERE discord_user_id=?`, account).Scan(&encrypted.Ciphertext, &encrypted.IV, &encrypted.AuthTag); err != nil {
		t.Fatal(err)
	}
	credentials, err := credentialcrypto.Decrypt(testMasterKey, account, encrypted)
	if err != nil {
		t.Fatal(err)
	}
	return credentials
}

// withFakeUADEHost points every outbound request whose target host is
// inscripcionespia.uade.edu.ar at server instead, without touching real DNS
// or making any request to the real UADE production host. sso.ValidStartURL
// (used by parseStartURL) hard-codes that exact host, so a healed start URL
// that satisfies it can never point at an httptest server directly -- this
// intercepts at the Transport/DialContext level and skips TLS hostname
// verification instead. Restores http.DefaultTransport on cleanup.
func withFakeUADEHost(t *testing.T, server *httptest.Server) {
	t.Helper()
	original := http.DefaultTransport
	dialer := &net.Dialer{}
	http.DefaultTransport = &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test-only, redirected to a local httptest server
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if host, _, err := net.SplitHostPort(addr); err == nil && strings.EqualFold(host, "inscripcionespia.uade.edu.ar") {
				addr = server.Listener.Addr().String()
			}
			return dialer.DialContext(ctx, network, addr)
		},
	}
	t.Cleanup(func() { http.DefaultTransport = original })
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "src", "automation", "__fixtures__", "webforms", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

const testHealedStartURL = "https://inscripcionespia.uade.edu.ar/InscripcionClaseBuscar.aspx?param=eyJhbGciOiJI"

// TestPollHealsStaleStartURLViaRelinkAndCompletesSearch covers the
// auto-sanación behavior from 03.3-15-PLAN.md: a poll() whose stored start
// URL is invalid, with Runtime.relink substituted by a fake returning a
// valid sso.Result, persists the new (encrypted) start URL and completes the
// search using that link instead of falling back to stale_start_url.
func TestPollHealsStaleStartURLViaRelinkAndCompletesSearch(t *testing.T) {
	initial := readFixture(t, "initial-form.html")
	found := readFixture(t, "postback-found.html")
	found = []byte(strings.Replace(string(found), `<table id="results"><tr class="row_central"><td>Física II</td><td>2 vacantes</td></tr></table>`, `<table id="results" class="grillaInscripcion"><tr class="row_central"><td class="tdTurno">MAÑANA</td><td class="tdSede">Lima</td><td class="tdHorario">08:00</td><td class="tdvacantes">2</td><td><input id="x_hiddenLU" value="True"><input id="x_hiddenMI" value="True"></td></tr></table>`, 1))

	fakeUADE := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, pass, ok := r.BasicAuth(); !ok || user != "old-user" || pass != "old-pass" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodGet {
			_, _ = w.Write(initial)
			return
		}
		_, _ = w.Write(found)
	}))
	defer fakeUADE.Close()
	withFakeUADEHost(t, fakeUADE)

	db := newTestRuntimeDB(t)
	const account = "user-heal"
	seedRuntimeAccount(t, db, account, "old-user", "old-pass", "https://inscripcionespia.uade.edu.ar/InscripcionClaseBuscar.aspx")
	jobID := seedRuntimeJob(t, db, account, `{"materiaCodigo":"3.1.050","ofrecimiento":"curricular","turno":"mañana","dias":["LU","MI"]}`)

	runtime, err := NewRuntime(context.Background(), db, testMasterKey, "", "https://inscripciones.uade.edu.ar/", time.Minute, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	runtime.relink = func(context.Context, *http.Client, string, string, string) (sso.Result, error) {
		return sso.Result{StartURL: testHealedStartURL}, nil
	}

	outcome, err := runtime.poll(context.Background(), jobID, account)
	if err != nil {
		t.Fatalf("poll returned unexpected error: %v", err)
	}
	if outcome.Code == "stale_start_url" {
		t.Fatalf("expected healed poll to not fall back to stale_start_url, got %+v", outcome)
	}
	if outcome.Code != "found" {
		t.Fatalf("expected the healed link to complete the search with a found outcome, got %+v", outcome)
	}

	if got := decryptRuntimeCredentials(t, db, account).UADEStartURL; got != testHealedStartURL {
		t.Fatalf("healed start url not persisted: got %q want %q", got, testHealedStartURL)
	}
}

// TestPollMarksNeedsManualStartURLAndFallsBackToStaleOnMFA covers the second
// auto-sanación edge from 03.3-15-PLAN.md: a poll() whose Runtime.relink fake
// returns sso.ErrMFARequired falls back to stale_start_url AND leaves
// pause_reason='needs_new_start_url' on the users row (via
// markNeedsManualStartURL), so the pause DM and /credenciales both reflect
// the real blocker.
func TestPollMarksNeedsManualStartURLAndFallsBackToStaleOnMFA(t *testing.T) {
	db := newTestRuntimeDB(t)
	const account = "user-mfa"
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
		t.Fatalf("expected stale_start_url fallback on MFA, got %+v", outcome)
	}

	var reason sql.NullString
	if err = db.QueryRow(`SELECT pause_reason FROM users WHERE discord_user_id=?`, account).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if reason.String != "needs_new_start_url" {
		t.Fatalf("pause_reason = %q, want needs_new_start_url", reason.String)
	}
}

// TestAttemptRelinkOnAccountReadyPersistsHealedStartURL calls
// attemptRelinkOnAccountReady directly (synchronously, same goroutine as the
// test) instead of through AccountReady's own launched goroutine, so the
// persistence assertion below has a real happens-before guarantee instead of
// racing a background goroutine's DB write from the test's goroutine.
// TestAccountReadyRunsRelinkInBackgroundWithoutBlockingCaller (below) proves
// the actual non-blocking/background-goroutine behavior of AccountReady
// itself.
func TestAttemptRelinkOnAccountReadyPersistsHealedStartURL(t *testing.T) {
	db := newTestRuntimeDB(t)
	const account = "user-ready-sync"
	seedRuntimeAccount(t, db, account, "u", "p", "https://inscripcionespia.uade.edu.ar/InscripcionClaseBuscar.aspx")

	runtime, err := NewRuntime(context.Background(), db, testMasterKey, "", "https://inscripciones.uade.edu.ar/", time.Minute, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	runtime.relink = func(context.Context, *http.Client, string, string, string) (sso.Result, error) {
		return sso.Result{StartURL: testHealedStartURL}, nil
	}

	runtime.attemptRelinkOnAccountReady(context.Background(), account)

	if got := decryptRuntimeCredentials(t, db, account).UADEStartURL; got != testHealedStartURL {
		t.Fatalf("healed start url not persisted: got %q want %q", got, testHealedStartURL)
	}
}

// TestAccountReadyRunsRelinkInBackgroundWithoutBlockingCaller proves
// AccountReady never makes its caller (submitCredentials, inside the
// interaction handler) wait for an SSO relink to finish: the fake relink
// blocks on a channel the test controls, and AccountReady must still return
// to its caller immediately. Synchronization uses only channels/select+
// time.After (same pattern as TestRecoverGoroutineConvertsPanicToLogInsteadOfCrashing),
// never a sleep.
func TestAccountReadyRunsRelinkInBackgroundWithoutBlockingCaller(t *testing.T) {
	db := newTestRuntimeDB(t)
	const account = "user-ready-async"
	seedRuntimeAccount(t, db, account, "u", "p", "https://inscripcionespia.uade.edu.ar/InscripcionClaseBuscar.aspx")

	runtime, err := NewRuntime(context.Background(), db, testMasterKey, "", "https://inscripciones.uade.edu.ar/", time.Minute, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	invoked := make(chan struct{})
	release := make(chan struct{})
	runtime.relink = func(context.Context, *http.Client, string, string, string) (sso.Result, error) {
		close(invoked) // signals the fake relink before returning, as instructed by the plan
		<-release
		return sso.Result{StartURL: testHealedStartURL}, nil
	}

	callerReturned := make(chan struct{})
	go func() {
		runtime.AccountReady(account)
		close(callerReturned)
	}()

	select {
	case <-callerReturned:
	case <-time.After(1 * time.Second):
		t.Fatal("AccountReady did not return to its caller within 1s")
	}

	select {
	case <-invoked:
	case <-time.After(1 * time.Second):
		t.Fatal("relink fake was not invoked in the background goroutine within 1s")
	}

	close(release)
}
