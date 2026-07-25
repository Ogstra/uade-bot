package discordhttp

import (
	"context"
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
