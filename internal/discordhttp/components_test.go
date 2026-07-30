package discordhttp

import (
	"bytes"
	"errors"
	"log"
	"strconv"
	"strings"
	"testing"
)

func componentInteraction(user, channelID, customID string) map[string]any {
	return map[string]any{
		"type":       3,
		"channel_id": channelID,
		"member": map[string]any{
			"user": map[string]any{"id": user},
		},
		"data": map[string]any{"custom_id": customID},
	}
}

func seedComponentJob(t *testing.T, d CommandDispatcher, owner, channelID, label string) int64 {
	t.Helper()
	if _, err := d.DB.Exec(`INSERT INTO users(discord_user_id,created_at,updated_at) VALUES(?,1,1)`, owner); err != nil {
		t.Fatal(err)
	}
	result, err := d.DB.Exec(`INSERT INTO jobs(discord_user_id,filtros_json,channel_id,label,status,created_at) VALUES(?,'{}',?,?,'active',1)`, owner, channelID, label)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func componentJobCount(t *testing.T, d CommandDispatcher, id int64) int {
	t.Helper()
	var count int
	if err := d.DB.QueryRow(`SELECT COUNT(*) FROM jobs WHERE id=?`, id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestComponentOwnerStopsJobAndNotifiesScheduler(t *testing.T) {
	d := testDispatcher(t)
	id := seedComponentJob(t, d, "owner", "channel", "Algoritmos")
	changed := 0
	d.OnJobsChanged = func() { changed++ }

	out := dispatchJSON(t, d, componentInteraction("owner", "channel", "detener_job:"+strconv.FormatInt(id, 10)))
	if !strings.Contains(responseContent(out), "detenida") {
		t.Fatalf("response = %q", responseContent(out))
	}
	if count := componentJobCount(t, d, id); count != 0 {
		t.Fatalf("job count = %d, want 0", count)
	}
	if changed != 1 {
		t.Fatalf("OnJobsChanged calls = %d, want 1", changed)
	}
}

func TestComponentOtherUserCannotStopJob(t *testing.T) {
	d := testDispatcher(t)
	id := seedComponentJob(t, d, "owner", "channel", "Algoritmos")
	changed := 0
	d.OnJobsChanged = func() { changed++ }

	out := dispatchJSON(t, d, componentInteraction("intruder", "channel", "detener_job:"+strconv.FormatInt(id, 10)))
	if !strings.Contains(responseContent(out), "no te pertenece") {
		t.Fatalf("response = %q", responseContent(out))
	}
	if count := componentJobCount(t, d, id); count != 1 {
		t.Fatalf("job count = %d, want 1", count)
	}
	if changed != 0 {
		t.Fatalf("OnJobsChanged calls = %d, want 0", changed)
	}
}

func TestComponentRejectsUnknownOrMalformedCustomID(t *testing.T) {
	for _, customID := range []string{"otro:1", "detener_job:no-numero"} {
		t.Run(customID, func(t *testing.T) {
			d := testDispatcher(t)
			id := seedComponentJob(t, d, "owner", "channel", "Algoritmos")
			out := dispatchJSON(t, d, componentInteraction("owner", "channel", customID))
			if responseContent(out) != "Boton desconocido." {
				t.Fatalf("response = %q", responseContent(out))
			}
			if count := componentJobCount(t, d, id); count != 1 {
				t.Fatalf("job count = %d, want 1", count)
			}
		})
	}
}

func TestComponentEchoesConfirmationToDifferentOriginalChannel(t *testing.T) {
	d := testDispatcher(t)
	id := seedComponentJob(t, d, "owner", "original-channel", "Algoritmos")
	var channel, content string
	calls := 0
	d.SendChannel = func(gotChannel, gotContent string) error {
		calls++
		channel, content = gotChannel, gotContent
		return nil
	}

	dispatchJSON(t, d, componentInteraction("owner", "dm-channel", "detener_job:"+strconv.FormatInt(id, 10)))
	if calls != 1 || channel != "original-channel" {
		t.Fatalf("calls = %d, channel = %q", calls, channel)
	}
	if !strings.Contains(content, strconv.FormatInt(id, 10)) || !strings.Contains(content, "Algoritmos") {
		t.Fatalf("echo content = %q", content)
	}
}

func TestComponentDoesNotEchoInOriginalChannel(t *testing.T) {
	d := testDispatcher(t)
	id := seedComponentJob(t, d, "owner", "original-channel", "Algoritmos")
	calls := 0
	d.SendChannel = func(string, string) error { calls++; return nil }

	dispatchJSON(t, d, componentInteraction("owner", "original-channel", "detener_job:"+strconv.FormatInt(id, 10)))
	if calls != 0 {
		t.Fatalf("SendChannel calls = %d, want 0", calls)
	}
}

func TestComponentEchoFailureDoesNotReplaceConfirmationOrLeakContent(t *testing.T) {
	originalWriter := log.Writer()
	originalFlags := log.Flags()
	var logs bytes.Buffer
	log.SetOutput(&logs)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(originalWriter)
		log.SetFlags(originalFlags)
	}()

	d := testDispatcher(t)
	id := seedComponentJob(t, d, "owner", "sensitive-channel", "sensitive-label")
	d.SendChannel = func(string, string) error { return errors.New("sensitive-error") }

	out := dispatchJSON(t, d, componentInteraction("owner", "dm-channel", "detener_job:"+strconv.FormatInt(id, 10)))
	if !strings.Contains(responseContent(out), "detenida") {
		t.Fatalf("response = %q", responseContent(out))
	}
	logged := logs.String()
	if !strings.Contains(logged, strconv.FormatInt(id, 10)) {
		t.Fatalf("log missing job id: %q", logged)
	}
	for _, prohibited := range []string{"sensitive-label", "sensitive-channel", "sensitive-error"} {
		if strings.Contains(logged, prohibited) {
			t.Fatalf("log leaked %q: %q", prohibited, logged)
		}
	}
}
