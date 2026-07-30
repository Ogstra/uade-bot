package dashboard

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ogs/uade-bot/internal/store"
)

func TestSnapshotProjectsSQLiteParityWithoutSecrets(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/dashboard.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`INSERT INTO users(discord_user_id,pause_reason,pause_until,backoff_attempt,last_pause_notified_reason,created_at,updated_at) VALUES ('user-a','needs_credentials',NULL,0,'SECRET_NOTIFY',1,1),('user-b',NULL,NULL,0,NULL,1,1);
	INSERT INTO credentials(discord_user_id,ciphertext,iv,auth_tag,updated_at) VALUES ('user-a','SECRET_CIPHER','SECRET_IV','SECRET_TAG',1);
	INSERT INTO materias(codigo,nombre,updated_at) VALUES ('1.1.010','Análisis Matemático',1);
	INSERT INTO jobs(id,discord_user_id,filtros_json,channel_id,label,status,last_polled_at,last_outcome,last_notified_state,created_at) VALUES (10,'user-a','{"materiaCodigo":"1.1.010","ofrecimiento":"optativa","turno":"Mañana","dias":["MA","JU"],"sedesExcluidas":["Costa Argentina"]}','SECRET_CHANNEL','Análisis','paused_by_user',300,'{"outcome":"no_vacancies","password":"SECRET_PASSWORD"}','SECRET_STATE',1),(20,'user-b','{"materiaCodigo":"1.1.020","ofrecimiento":"curricular","turno":"Noche","dias":["LU"],"sedesExcluidas":[]}',NULL,'Álgebra','active',400,'{"outcome":"found","vacancies":[{"cupos":4}]}',NULL,1);
	INSERT INTO poll_outcome_history(id,job_id,recorded_at,outcome_code,vacancy_count,total_cupos) VALUES (1,10,300,'no_vacancies',NULL,NULL),(2,20,200,'found',1,4),(3,20,400,'search_failed',NULL,NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := (SnapshotSource{DB: db, Now: func() time.Time { return time.UnixMilli(500) }, DisplayNames: map[string]string{"user-a": "Ana"}, Guilds: []Guild{{ID: "guild-a", Name: "Servidor <script>"}}}).Build()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.GeneratedAt != 500 || snapshot.Health.ActiveAccounts != 1 || snapshot.Health.PausedAccounts.Total != 1 || snapshot.Health.Jobs.Total != 2 || snapshot.Health.LastSuccessfulPollAt == nil || *snapshot.Health.LastSuccessfulPollAt != 400 {
		t.Fatalf("health=%+v", snapshot.Health)
	}
	if len(snapshot.Accounts) != 2 || snapshot.Accounts[0].DisplayName != "Ana" || snapshot.Accounts[0].Jobs[0].Filters.MateriaNombre == nil || *snapshot.Accounts[0].Jobs[0].Filters.MateriaNombre != "Análisis Matemático" {
		t.Fatalf("accounts=%+v", snapshot.Accounts)
	}
	if snapshot.Accounts[1].Jobs[0].Outcome.Code != "found" || *snapshot.Accounts[1].Jobs[0].Outcome.TotalCupos != 4 {
		t.Fatalf("outcome=%+v", snapshot.Accounts[1].Jobs[0].Outcome)
	}
	encoded, _ := json.Marshal(snapshot)
	serialized := string(encoded)
	for _, secret := range []string{"SECRET_", "ciphertext", "channelId", "lastOutcome", "lastNotifiedState", "password"} {
		if strings.Contains(serialized, secret) {
			t.Errorf("snapshot leaked %q: %s", secret, serialized)
		}
	}
}

func TestSnapshotHasStableEmptyArraysAndUnknownSafeProjection(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/dashboard.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, _ = db.Exec(`INSERT INTO users(discord_user_id,pause_reason,backoff_attempt,created_at,updated_at) VALUES('unused','<script>bad</script>',0,1,1)`)
	snapshot, err := (SnapshotSource{DB: db, Now: func() time.Time { return time.UnixMilli(700) }}).Build()
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(snapshot)
	text := string(encoded)
	for _, want := range []string{`"botGuilds":[]`, `"accounts":[]`, `"code":"unknown"`, `"lastSuccessfulPollAt":null`} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %s in %s", want, text)
		}
	}
	if strings.Contains(text, "<script>") {
		t.Fatal("raw pause reason leaked")
	}
}

func TestSnapshotProvidersAreReadOnEveryBuildAndUseRawIDFallback(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/dashboard.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`INSERT INTO users(discord_user_id,pause_reason,backoff_attempt,created_at,updated_at) VALUES('123',NULL,0,1,1);
		INSERT INTO jobs(id,discord_user_id,filtros_json,status,created_at) VALUES(1,'123','{"materiaCodigo":"1.1.010","turno":"Noche"}','active',1)`)
	if err != nil {
		t.Fatal(err)
	}
	guilds := []Guild{{ID: "b", Name: "Zulu"}, {ID: "a", Name: "Alpha"}}
	names := map[string]string{}
	source := SnapshotSource{
		DB:            db,
		GuildProvider: func() []Guild { return append([]Guild(nil), guilds...) },
		DisplayNameProvider: func() map[string]string {
			out := make(map[string]string, len(names))
			for key, value := range names {
				out[key] = value
			}
			return out
		},
	}
	first, err := source.Build()
	if err != nil {
		t.Fatal(err)
	}
	if len(first.BotGuilds) != 2 || first.BotGuilds[0].Name != "Alpha" {
		t.Fatalf("first guilds = %+v", first.BotGuilds)
	}
	if len(first.Accounts) != 1 || first.Accounts[0].DisplayName != "123" {
		t.Fatalf("raw ID fallback missing: %+v", first.Accounts)
	}

	guilds = append(guilds, Guild{ID: "c", Name: "Beta"})
	names["123"] = "Ana"
	second, err := source.Build()
	if err != nil {
		t.Fatal(err)
	}
	if len(second.BotGuilds) != 3 || second.BotGuilds[1].Name != "Beta" || second.Accounts[0].DisplayName != "Ana" {
		t.Fatalf("providers were frozen: guilds=%+v accounts=%+v", second.BotGuilds, second.Accounts)
	}
}
