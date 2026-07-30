package discordhttp

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	credentialcrypto "github.com/ogs/uade-bot/internal/crypto"
	"github.com/ogs/uade-bot/internal/store"
)

const testMaster = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

func testDispatcher(t *testing.T) CommandDispatcher {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return CommandDispatcher{DB: db, MasterKey: testMaster}
}
func dispatchJSON(t *testing.T, d CommandDispatcher, payload any) InteractionResponse {
	t.Helper()
	body, _ := json.Marshal(payload)
	out, err := d.Dispatch(context.Background(), body)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func command(user, name, perms string, options []map[string]any) map[string]any {
	return map[string]any{"type": 2, "guild_id": "any-guild", "channel_id": "any-channel", "member": map[string]any{"permissions": perms, "user": map[string]any{"id": user}}, "data": map[string]any{"name": name, "options": options}}
}
func credentialsSubmit(user, username, password string) map[string]any {
	field := func(id, value string) any {
		return map[string]any{"components": []any{map[string]any{"custom_id": id, "value": value}}}
	}
	return map[string]any{"type": 5, "member": map[string]any{"user": map[string]any{"id": user}}, "data": map[string]any{"custom_id": "credentials", "components": []any{field("uade_username", username), field("uade_password", password)}}}
}
func seedCredentials(t *testing.T, d CommandDispatcher, user, username, password, link string) {
	t.Helper()
	encrypted, err := credentialcrypto.Encrypt(testMaster, user, credentialcrypto.Credentials{UADEUsername: username, UADEPassword: password, UADEStartURL: link})
	if err != nil {
		t.Fatal(err)
	}
	// Dos Exec separados a proposito: modernc.org/sqlite no reparte de forma
	// confiable los placeholders entre varios statements en un mismo Exec, y
	// hacerlo dejaba la fila de credenciales con valores corridos (Decrypt
	// fallaba con "illegal base64 data").
	if _, err = d.DB.Exec(`INSERT INTO users(discord_user_id,created_at,updated_at) VALUES(?,1,1)`, user); err != nil {
		t.Fatal(err)
	}
	if _, err = d.DB.Exec(`INSERT INTO credentials(discord_user_id,ciphertext,iv,auth_tag,updated_at) VALUES(?,?,?,?,1)`, user, encrypted.Ciphertext, encrypted.IV, encrypted.AuthTag); err != nil {
		t.Fatal(err)
	}
}

func TestGlobalCommandsContainsExactlyTwelveRequiredCommands(t *testing.T) {
	want := []string{"buscar", "estado", "detener", "pausar", "reanudar", "credenciales", "admin-estado", "admin-detener", "admin-pausar", "admin-reanudar", "admin-stats", "admin-user-stats"}
	commands := GlobalCommands()
	if len(commands) != len(want) {
		t.Fatalf("got %d", len(commands))
	}
	for i, name := range want {
		if commands[i].Name != name {
			t.Fatalf("command %d = %s", i, commands[i].Name)
		}
	}
}

func TestRegisterGlobalUsesOnlyApplicationGlobalEndpointAndRetries429(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if strings.Contains(r.URL.Path, "guild") {
			t.Errorf("guild route: %s", r.URL.Path)
		}
		if r.URL.Path != "/applications/app/commands" {
			t.Errorf("path %s", r.URL.Path)
		}
		if r.Method != http.MethodPut {
			t.Errorf("method %s", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "DISCORD_GUILD_ID") {
			t.Error("guild id in body")
		}
		if calls == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(429)
			return
		}
		w.WriteHeader(200)
	}))
	defer server.Close()
	if err := RegisterGlobal(context.Background(), server.Client(), server.URL, "token", "app"); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls %d", calls)
	}
}

func TestCredentialModalEncryptsWithoutEchoingSecrets(t *testing.T) {
	d := testDispatcher(t)
	const startURL = "https://inscripcionespia.uade.edu.ar/a?param=secret-link"
	seedCredentials(t, d, "u1", "old-user", "old-pass", startURL)
	modal := dispatchJSON(t, d, command("u1", "credenciales", "0", nil))
	if modal.Type != 9 {
		t.Fatalf("type %d", modal.Type)
	}
	modalData := modal.Data.(map[string]any)
	if components := modalData["components"].([]any); len(components) != 2 {
		t.Fatalf("modal has %d fields, want 2", len(components))
	}
	payload := credentialsSubmit("u1", "secret-user", "secret-pass")
	out := dispatchJSON(t, d, payload)
	if !strings.Contains(responseContent(out), "guardadas") {
		t.Fatalf("modal response: %s", responseContent(out))
	}
	encoded, _ := json.Marshal(out)
	for _, secret := range []string{"secret-user", "secret-pass", "secret-link"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("secret echoed: %s", secret)
		}
	}
	var ciphertext string
	if err := d.DB.QueryRow(`SELECT ciphertext FROM credentials WHERE discord_user_id='u1'`).Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ciphertext, "secret") {
		t.Fatal("plaintext persisted")
	}
	stored, err := d.readCredentials(context.Background(), "u1")
	if err != nil || stored.UADEStartURL != startURL {
		t.Fatalf("stored link changed: %q err=%v", stored.UADEStartURL, err)
	}
}

func TestOwnershipAdminPermissionAndCommandDispatch(t *testing.T) {
	d := testDispatcher(t)
	// Seed users/credentials through modal, then create a job.
	seedCredentials(t, d, "owner", "u", "p", "https://inscripcionespia.uade.edu.ar/x?param=v")
	options := []map[string]any{{"name": "cod_materia", "value": "3.1.050"}, {"name": "turno", "value": "Noche"}, {"name": "ofrecimiento", "value": "curricular"}, {"name": "dias", "value": "LU"}}
	if out := dispatchJSON(t, d, command("owner", "buscar", "0", options)); out.Type != 4 {
		t.Fatal(out.Type)
	}
	if out := dispatchJSON(t, d, command("intruder", "detener", "0", []map[string]any{{"name": "busqueda", "value": "1"}})); !strings.Contains(responseContent(out), "no te pertenece") {
		t.Fatalf("%s", responseContent(out))
	}
	if out := dispatchJSON(t, d, command("intruder", "admin-detener", "0", []map[string]any{{"name": "busqueda", "value": "1"}})); !strings.Contains(responseContent(out), "Administrador") {
		t.Fatalf("%s", responseContent(out))
	}
	if out := dispatchJSON(t, d, command("admin", "admin-detener", "8", []map[string]any{{"name": "busqueda", "value": "1"}})); !strings.Contains(responseContent(out), "detenida") {
		t.Fatalf("%s", responseContent(out))
	}
}

func TestBuscarPersistsNodeCompatibleContractAndLifecycle(t *testing.T) {
	d := testDispatcher(t)
	seedCredentials(t, d, "owner", "u", "p", "https://inscripcionespia.uade.edu.ar/x?param=v")
	created := ""
	changed := 0
	d.OnJobCreated = func(id string) { created = id }
	d.OnJobsChanged = func() { changed++ }
	options := []map[string]any{{"name": "cod_materia", "value": "3.1.050"}, {"name": "turno", "value": "Noche"}, {"name": "ofrecimiento", "value": "curricular"}, {"name": "dias", "value": "lu, MI"}, {"name": "sedes_excluidas", "value": "Lima, Monserrat"}}
	dispatchJSON(t, d, command("owner", "buscar", "0", options))
	var raw, channel, guild, label, status string
	if err := d.DB.QueryRow(`SELECT filtros_json,channel_id,guild_id,label,status FROM jobs WHERE id=1`).Scan(&raw, &channel, &guild, &label, &status); err != nil {
		t.Fatal(err)
	}
	if created != "1" || channel != "any-channel" || guild != "any-guild" || label != "3.1.050" || status != "active" {
		t.Fatalf("created=%s channel=%s guild=%s label=%s status=%s", created, channel, guild, label, status)
	}
	var stored map[string]any
	if json.Unmarshal([]byte(raw), &stored) != nil || stored["materiaCodigo"] != "3.1.050" {
		t.Fatalf("filters=%s", raw)
	}
	if dias, ok := stored["dias"].([]any); !ok || len(dias) != 2 || dias[0] != "LU" || dias[1] != "MI" {
		t.Fatalf("dias=%v", stored["dias"])
	}
	dispatchJSON(t, d, command("owner", "pausar", "0", []map[string]any{{"name": "busqueda", "value": "1"}}))
	if err := d.DB.QueryRow(`SELECT status FROM jobs WHERE id=1`).Scan(&status); err != nil || status != "paused_by_user" {
		t.Fatalf("status=%s err=%v", status, err)
	}
	dispatchJSON(t, d, command("owner", "detener", "0", []map[string]any{{"name": "busqueda", "value": "1"}}))
	var count int
	_ = d.DB.QueryRow(`SELECT COUNT(*) FROM jobs WHERE id=1`).Scan(&count)
	if count != 0 || changed != 2 {
		t.Fatalf("count=%d changed=%d", count, changed)
	}
}

func TestBuscarResolvesMateriaPersistsBeforeCallbackAndConfirmsMonitoring(t *testing.T) {
	d := testDispatcher(t)
	seedCredentials(t, d, "owner", "u", "p", "https://inscripcionespia.uade.edu.ar/x?param=v")
	resolveCalls := 0
	d.ResolveMateria = func(_ context.Context, user, code string) (string, error) {
		resolveCalls++
		if user != "owner" || code != "3.1.050" {
			t.Fatalf("resolver args=%s/%s", user, code)
		}
		return "PROGRAMACIÓN 2", nil
	}
	d.OnJobCreated = func(string) {
		var name string
		if err := d.DB.QueryRow(`SELECT nombre FROM materias WHERE codigo='3.1.050'`).Scan(&name); err != nil || name != "PROGRAMACIÓN 2" {
			t.Fatalf("callback before materia commit: %q %v", name, err)
		}
	}
	options := []map[string]any{{"name": "cod_materia", "value": "3.1.050"}, {"name": "turno", "value": "Noche"}, {"name": "ofrecimiento", "value": "curricular"}, {"name": "dias", "value": "LU"}}
	content := responseContent(dispatchJSON(t, d, command("owner", "buscar", "0", options)))
	for _, want := range []string{"3.1.050", "PROGRAMACIÓN 2", "Monitoreo", "activo"} {
		if !strings.Contains(content, want) {
			t.Fatalf("missing %q: %s", want, content)
		}
	}
	if strings.Contains(content, "El scheduler hará el primer intento sin bloquear esta interacción.") {
		t.Fatalf("leaked scheduler detail: %s", content)
	}
	if resolveCalls != 1 {
		t.Fatalf("resolver calls=%d", resolveCalls)
	}

	// A clean cache hit must avoid UADE resolution.
	dispatchJSON(t, d, command("owner", "buscar", "0", options))
	if resolveCalls != 1 {
		t.Fatalf("cache hit called resolver: %d", resolveCalls)
	}
}

// Un usuario sin credenciales que corre /buscar recibe el modal directamente en
// vez de un texto pidiendole que descubra y tipee /credenciales.
func TestBuscarWithoutCredentialsOpensModalDirectly(t *testing.T) {
	d := testDispatcher(t)
	searchOptions := []map[string]any{{"name": "cod_materia", "value": "3.1.050"}, {"name": "turno", "value": "Noche"}, {"name": "ofrecimiento", "value": "curricular"}, {"name": "dias", "value": "LU"}}
	out := dispatchJSON(t, d, command("sin-credenciales", "buscar", "0", searchOptions))
	if out.Type != 9 {
		t.Fatalf("buscar sin credenciales debe abrir el modal (type 9), got type %d", out.Type)
	}
	data, ok := out.Data.(map[string]any)
	if !ok {
		t.Fatalf("modal data no es un mapa: %T", out.Data)
	}
	if data["custom_id"] != "credentials" {
		t.Fatalf("custom_id = %v, want credentials", data["custom_id"])
	}
	components, ok := data["components"].([]any)
	if !ok || len(components) != 2 {
		t.Fatalf("modal tiene %d campos, want 2 (usuario y password)", len(components))
	}
	var raw, channel, guild, label string
	if err := d.DB.QueryRow(`SELECT filtros_json,channel_id,guild_id,label FROM pending_searches WHERE discord_user_id=?`, "sin-credenciales").Scan(&raw, &channel, &guild, &label); err != nil {
		t.Fatal(err)
	}
	if channel != "any-channel" || guild != "any-guild" || label != "3.1.050" {
		t.Fatalf("channel=%s guild=%s label=%s", channel, guild, label)
	}
	var stored map[string]any
	if json.Unmarshal([]byte(raw), &stored) != nil || stored["materiaCodigo"] != "3.1.050" {
		t.Fatalf("filters=%s", raw)
	}
}

// Completar el modal de credenciales con una búsqueda pendiente materializa
// esa búsqueda exacta como job activo, en vez de pedirle al usuario que
// repita /buscar.
func TestSubmitCredentialsMaterializesPendingSearch(t *testing.T) {
	d := testDispatcher(t)
	searchOptions := []map[string]any{{"name": "cod_materia", "value": "3.1.050"}, {"name": "turno", "value": "Noche"}, {"name": "ofrecimiento", "value": "curricular"}, {"name": "dias", "value": "LU"}}
	created := ""
	d.OnJobCreated = func(id string) { created = id }
	out := dispatchJSON(t, d, command("sin-credenciales", "buscar", "0", searchOptions))
	if out.Type != 9 {
		t.Fatalf("buscar sin credenciales debe abrir el modal, got type %d", out.Type)
	}
	submit := dispatchJSON(t, d, credentialsSubmit("sin-credenciales", "user", "pass"))
	if content := responseContent(submit); !strings.Contains(content, "Ya creé la búsqueda") {
		t.Fatalf("respuesta no menciona la búsqueda ya creada: %s", content)
	}
	if content := responseContent(submit); !strings.Contains(content, "**Materia:**") {
		t.Fatalf("respuesta no incluye el resumen de filtros: %s", content)
	}
	if created == "" {
		t.Fatal("OnJobCreated no se disparó")
	}
	var jobCount int
	if err := d.DB.QueryRow(`SELECT COUNT(*) FROM jobs WHERE discord_user_id=? AND status='active'`, "sin-credenciales").Scan(&jobCount); err != nil {
		t.Fatal(err)
	}
	if jobCount != 1 {
		t.Fatalf("jobs activos=%d, want 1", jobCount)
	}
	var raw string
	if err := d.DB.QueryRow(`SELECT filtros_json FROM jobs WHERE discord_user_id=?`, "sin-credenciales").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var stored map[string]any
	if json.Unmarshal([]byte(raw), &stored) != nil || stored["materiaCodigo"] != "3.1.050" {
		t.Fatalf("filters=%s", raw)
	}
	var pendingCount int
	if err := d.DB.QueryRow(`SELECT COUNT(*) FROM pending_searches WHERE discord_user_id=?`, "sin-credenciales").Scan(&pendingCount); err != nil {
		t.Fatal(err)
	}
	if pendingCount != 0 {
		t.Fatalf("pending_searches=%d, want 0", pendingCount)
	}
}

func TestEstadoShowsDetailedJobStatusAndSuppressesMentions(t *testing.T) {
	d := testDispatcher(t)
	seedCredentials(t, d, "owner", "u", "p", "https://inscripcionespia.uade.edu.ar/x?param=v")
	if _, err := d.DB.Exec(`INSERT INTO materias(codigo,nombre,updated_at) VALUES('3.1.050','Algoritmos',1)`); err != nil {
		t.Fatal(err)
	}
	options := []map[string]any{
		{"name": "cod_materia", "value": "3.1.050"},
		{"name": "turno", "value": "Noche"},
		{"name": "ofrecimiento", "value": "curricular"},
		{"name": "dias", "value": "LU,MI"},
		{"name": "sedes_excluidas", "value": "Lima,Monserrat"},
		{"name": "etiqueta", "value": "Algoritmos nocturna"},
	}
	dispatchJSON(t, d, command("owner", "buscar", "0", options))
	if _, err := d.DB.Exec(`UPDATE jobs SET last_polled_at=?,last_outcome='found' WHERE id=1`, int64(1785434645000)); err != nil {
		t.Fatal(err)
	}
	if _, err := d.DB.Exec(`INSERT INTO poll_outcome_history(job_id,recorded_at,outcome_code,vacancy_count,total_cupos) VALUES(1,2,'found',1,7)`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.DB.Exec(`UPDATE users SET pause_reason='rate_limited' WHERE discord_user_id='owner'`); err != nil {
		t.Fatal(err)
	}

	out := dispatchJSON(t, d, command("owner", "estado", "0", nil))
	content := responseContent(out)
	for _, want := range []string{
		"**Materia:**", "3.1.050 - Algoritmos", "**Turno:** Noche", "**Dias:** LU, MI",
		"**Sedes excluidas:** Lima, Monserrat", "**Server/Canal:** <#any-channel>",
		"**Estado:** pausada (rate_limited)", "**Ultimo sondeo:**", "**Ultimo resultado:** vacante encontrada (7 cupos)",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("/estado no contiene %q: %s", want, content)
		}
	}
	data := out.Data.(map[string]any)
	mentions, ok := data["allowed_mentions"].(map[string]any)
	if !ok {
		t.Fatalf("allowed_mentions ausente: %#v", data)
	}
	parse, ok := mentions["parse"].([]string)
	if !ok || len(parse) != 0 {
		t.Fatalf("allowed_mentions.parse = %#v, want []", mentions["parse"])
	}
}

func TestAdminEstadoUsesCompactLinesAndIgnoresAccountPauseReason(t *testing.T) {
	d := testDispatcher(t)
	seedCredentials(t, d, "u1", "u", "p", "https://inscripcionespia.uade.edu.ar/x?param=v")
	seedCredentials(t, d, "u2", "u", "p", "https://inscripcionespia.uade.edu.ar/x?param=v")
	if _, err := d.DB.Exec(`INSERT INTO materias(codigo,nombre,updated_at) VALUES('3.1.050','Algoritmos',1)`); err != nil {
		t.Fatal(err)
	}
	filters := `{"materiaCodigo":"3.1.050","turno":"Noche","ofrecimiento":"curricular","dias":["LU","MI"],"sedesExcluidas":[]}`
	if _, err := d.DB.Exec(`INSERT INTO jobs(discord_user_id,filtros_json,channel_id,guild_id,label,status,last_outcome,created_at) VALUES('u1',?,'c1','g1','Mi etiqueta','active','no_vacancies',1),('u2',?,'c2','g2','3.1.050','paused_by_user','search_failed',2)`, filters, filters); err != nil {
		t.Fatal(err)
	}
	if _, err := d.DB.Exec(`UPDATE users SET pause_reason='needs_credentials' WHERE discord_user_id='u1'`); err != nil {
		t.Fatal(err)
	}

	content := responseContent(dispatchJSON(t, d, command("admin", "admin-estado", "8", nil)))
	for _, want := range []string{
		"**#1** <@u1> · Mi etiqueta - 3.1.050 - Algoritmos · Noche LU/MI · activa · <#c1> · sin vacantes",
		"**#2** <@u2> · 3.1.050 - Algoritmos · Noche LU/MI · pausada · <#c2> · fallo la busqueda",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("/admin-estado no contiene %q: %s", want, content)
		}
	}
	if strings.Contains(content, "needs_credentials") {
		t.Fatalf("la linea admin no debe reflejar pause_reason de cuenta: %s", content)
	}
	if len(content) > 1900 {
		t.Fatalf("respuesta admin demasiado larga: %d", len(content))
	}
}

func TestPendingSearchAppearsInEstado(t *testing.T) {
	d := testDispatcher(t)
	options := []map[string]any{
		{"name": "cod_materia", "value": "3.1.050"},
		{"name": "turno", "value": "Noche"},
		{"name": "ofrecimiento", "value": "curricular"},
		{"name": "dias", "value": "LU"},
		{"name": "etiqueta", "value": "Pendiente especial"},
	}
	if out := dispatchJSON(t, d, command("u-pending", "buscar", "0", options)); out.Type != 9 {
		t.Fatalf("buscar sin credenciales type=%d, want modal", out.Type)
	}
	content := responseContent(dispatchJSON(t, d, command("u-pending", "estado", "0", nil)))
	if !strings.Contains(content, "Pendiente especial") || !strings.Contains(content, "pendiente") {
		t.Fatalf("búsqueda pendiente no visible: %s", content)
	}
}

func TestBuscarRejectsWhenNoValidDaysWithoutWriting(t *testing.T) {
	d := testDispatcher(t)
	options := []map[string]any{
		{"name": "cod_materia", "value": "3.1.050"},
		{"name": "turno", "value": "Noche"},
		{"name": "ofrecimiento", "value": "curricular"},
		{"name": "dias", "value": "XX,YY"},
	}
	content := responseContent(dispatchJSON(t, d, command("u-invalid", "buscar", "0", options)))
	for _, day := range []string{"LU", "MA", "MI", "JU", "VI", "SA"} {
		if !strings.Contains(content, day) {
			t.Fatalf("respuesta no menciona %s: %s", day, content)
		}
	}
	for _, table := range []string{"jobs", "pending_searches"} {
		var count int
		if err := d.DB.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s count=%d err=%v, want 0", table, count, err)
		}
	}
}

func TestBuscarFiltersInvalidDaysAndShowsSummary(t *testing.T) {
	d := testDispatcher(t)
	seedCredentials(t, d, "owner", "u", "p", "https://inscripcionespia.uade.edu.ar/x?param=v")
	if _, err := d.DB.Exec(`INSERT INTO materias(codigo,nombre,updated_at) VALUES('3.1.050','Algoritmos',1)`); err != nil {
		t.Fatal(err)
	}
	options := []map[string]any{
		{"name": "cod_materia", "value": "3.1.050"},
		{"name": "turno", "value": "Noche"},
		{"name": "ofrecimiento", "value": "curricular"},
		{"name": "dias", "value": "lu,xx,MI"},
	}
	out := dispatchJSON(t, d, command("owner", "buscar", "0", options))
	for _, want := range []string{"**Materia:**", "**Turno:**", "**Ofrecimiento:**", "**Dias:**"} {
		if !strings.Contains(responseContent(out), want) {
			t.Fatalf("respuesta no contiene %q: %s", want, responseContent(out))
		}
	}
	var raw string
	if err := d.DB.QueryRow(`SELECT filtros_json FROM jobs WHERE discord_user_id='owner'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	filters := parseJobFilters(raw)
	if got := strings.Join(filters.Dias, ","); got != "LU,MI" {
		t.Fatalf("dias persistidos=%q, want LU,MI", got)
	}
}

func TestAllTwelveCommandDispatchersAcknowledge(t *testing.T) {
	d := testDispatcher(t)
	seedCredentials(t, d, "owner", "u", "p", "https://inscripcionespia.uade.edu.ar/x?param=v")
	searchOptions := []map[string]any{{"name": "cod_materia", "value": "3.1.050"}, {"name": "turno", "value": "Noche"}, {"name": "ofrecimiento", "value": "curricular"}, {"name": "dias", "value": "LU"}}
	cases := []struct {
		name, user, permissions string
		options                 []map[string]any
		wantType                int
	}{
		{"buscar", "owner", "0", searchOptions, 4}, {"estado", "owner", "0", nil, 4},
		{"detener", "owner", "0", []map[string]any{{"name": "busqueda", "value": "999"}}, 4},
		{"pausar", "owner", "0", []map[string]any{{"name": "busqueda", "value": "999"}}, 4},
		{"reanudar", "owner", "0", []map[string]any{{"name": "busqueda", "value": "999"}}, 4},
		{"credenciales", "owner", "0", nil, 9}, {"admin-estado", "admin", "8", nil, 4},
		{"admin-detener", "admin", "8", []map[string]any{{"name": "busqueda", "value": "999"}}, 4},
		{"admin-pausar", "admin", "8", []map[string]any{{"name": "busqueda", "value": "999"}}, 4},
		{"admin-reanudar", "admin", "8", []map[string]any{{"name": "busqueda", "value": "999"}}, 4},
		{"admin-stats", "admin", "8", nil, 4},
		{"admin-user-stats", "admin", "8", []map[string]any{{"name": "usuario", "value": "owner"}}, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := dispatchJSON(t, d, command(tc.user, tc.name, tc.permissions, tc.options))
			if out.Type != tc.wantType {
				t.Fatalf("type %d", out.Type)
			}
		})
	}
}

// /credenciales para una cuenta con pause_reason='needs_new_start_url'
// devuelve el modal de link manual, no el modal normal de usuario/password.
func TestCredencialesShowsManualLinkModalWhenAccountNeedsNewStartURL(t *testing.T) {
	d := testDispatcher(t)
	seedCredentials(t, d, "u1", "user", "pass", "https://inscripcionespia.uade.edu.ar/x?param=v")
	if _, err := d.DB.Exec(`UPDATE users SET pause_reason='needs_new_start_url' WHERE discord_user_id=?`, "u1"); err != nil {
		t.Fatal(err)
	}
	out := dispatchJSON(t, d, command("u1", "credenciales", "0", nil))
	if out.Type != 9 {
		t.Fatalf("type %d", out.Type)
	}
	data := out.Data.(map[string]any)
	if data["custom_id"] != "sso_manual_link" {
		t.Fatalf("custom_id = %v, want sso_manual_link", data["custom_id"])
	}
	components := data["components"].([]any)
	if len(components) != 1 {
		t.Fatalf("modal tiene %d campos, want 1", len(components))
	}
}

func manualLinkSubmit(user, link string) map[string]any {
	field := func(id, value string) any {
		return map[string]any{"components": []any{map[string]any{"custom_id": id, "value": value}}}
	}
	return map[string]any{"type": 5, "member": map[string]any{"user": map[string]any{"id": user}}, "data": map[string]any{"custom_id": "sso_manual_link", "components": []any{field("start_url", link)}}}
}

// Un link manual inválido no persiste nada ni limpia pause_reason; uno
// válido actualiza el start URL cifrado, limpia pause_reason y reactiva.
func TestSubmitManualStartURLValidatesAndReactivatesAccount(t *testing.T) {
	d := testDispatcher(t)
	seedCredentials(t, d, "u1", "user", "pass", "https://inscripcionespia.uade.edu.ar/x?param=old")
	if _, err := d.DB.Exec(`UPDATE users SET pause_reason='needs_new_start_url' WHERE discord_user_id=?`, "u1"); err != nil {
		t.Fatal(err)
	}

	badOut := dispatchJSON(t, d, manualLinkSubmit("u1", "http://otrohost.com/x?param=v"))
	if !strings.Contains(responseContent(badOut), "no parece válido") {
		t.Fatalf("respuesta inesperada: %s", responseContent(badOut))
	}
	var reason sql.NullString
	if err := d.DB.QueryRow(`SELECT pause_reason FROM users WHERE discord_user_id=?`, "u1").Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if reason.String != "needs_new_start_url" {
		t.Fatalf("pause_reason cambió tras link inválido: %v", reason)
	}
	stillOld, err := d.readCredentials(context.Background(), "u1")
	if err != nil || stillOld.UADEStartURL != "https://inscripcionespia.uade.edu.ar/x?param=old" {
		t.Fatalf("start url cambió tras link inválido: %q err=%v", stillOld.UADEStartURL, err)
	}

	const newLink = "https://inscripcionespia.uade.edu.ar/x?param=new-secret-link"
	changed := 0
	d.OnJobsChanged = func() { changed++ }
	goodOut := dispatchJSON(t, d, manualLinkSubmit("u1", newLink))
	if !strings.Contains(responseContent(goodOut), "Reactivé") {
		t.Fatalf("respuesta inesperada: %s", responseContent(goodOut))
	}
	encoded, _ := json.Marshal(goodOut)
	if strings.Contains(string(encoded), "new-secret-link") {
		t.Fatal("link completo ecoado en la respuesta")
	}
	if changed != 1 {
		t.Fatalf("OnJobsChanged calls=%d, want 1", changed)
	}
	if err := d.DB.QueryRow(`SELECT pause_reason FROM users WHERE discord_user_id=?`, "u1").Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if reason.Valid {
		t.Fatalf("pause_reason no quedó NULL: %v", reason)
	}
	updated, err := d.readCredentials(context.Background(), "u1")
	if err != nil || updated.UADEStartURL != newLink {
		t.Fatalf("start url no se actualizó: %q err=%v", updated.UADEStartURL, err)
	}
}

func responseContent(response InteractionResponse) string {
	data, _ := response.Data.(map[string]any)
	value, _ := data["content"].(string)
	return value
}

// TestDispatchInteractionMatchesDispatchForEquivalentPayload proves the
// Dispatch/DispatchInteraction split preserves behavior: constructing an
// Interaction directly (as internal/discordgateway will do) produces the
// same InteractionResponse as marshaling the equivalent payload through
// Dispatch's JSON wrapper. This is the seam Task 2 depends on.
func TestDispatchInteractionMatchesDispatchForEquivalentPayload(t *testing.T) {
	d := testDispatcher(t)
	seedCredentials(t, d, "owner", "u", "p", "https://inscripcionespia.uade.edu.ar/x?param=v")

	viaJSON := dispatchJSON(t, d, command("owner", "estado", "0", nil))

	viaStruct, err := d.DispatchInteraction(context.Background(), Interaction{
		Type:      2,
		GuildID:   "any-guild",
		ChannelID: "any-channel",
		Member:    Member{Permissions: "0", User: User{ID: "owner"}},
		Data:      InteractionData{Name: "estado"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if viaStruct.Type != viaJSON.Type || responseContent(viaStruct) != responseContent(viaJSON) {
		t.Fatalf("DispatchInteraction produced %+v, want %+v", viaStruct, viaJSON)
	}
}
