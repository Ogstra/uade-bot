package app

import (
	"bytes"
	"context"
	"crypto/tls"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/disgoorg/disgo/discord"
	credentialcrypto "github.com/ogs/uade-bot/internal/crypto"
	"github.com/ogs/uade-bot/internal/discordrest"
	"github.com/ogs/uade-bot/internal/scheduler"
	"github.com/ogs/uade-bot/internal/sso"
	"github.com/ogs/uade-bot/internal/store"
)

type fakeDiscordSender struct {
	dmCalls         []discordSend
	channelCalls    []discordSend
	dmFailures      map[int]error
	channelFailures map[int]error
	now             time.Time
	nonceTTL        time.Duration
	nonceExpiry     map[string]time.Time
	created         []discordSend
}

type discordSend struct {
	target     string
	content    string
	nonce      string
	components []discord.ContainerComponent
}

func (f *fakeDiscordSender) SendDM(user, content, nonce string, components ...discord.ContainerComponent) error {
	return f.send(notificationRouteDM, user, content, nonce, components)
}

func (f *fakeDiscordSender) SendMention(channel, owner, content, nonce string, components ...discord.ContainerComponent) error {
	return f.send(notificationRouteChannel, channel, content, nonce, components)
}

func (f *fakeDiscordSender) send(route notificationRoute, target, content, nonce string, components []discord.ContainerComponent) error {
	call := discordSend{target: target, content: content, nonce: nonce, components: components}
	var calls *[]discordSend
	var failures map[int]error
	if route == notificationRouteDM {
		calls, failures = &f.dmCalls, f.dmFailures
	} else {
		calls, failures = &f.channelCalls, f.channelFailures
	}
	*calls = append(*calls, call)
	if err := failures[len(*calls)-1]; err != nil {
		return err
	}
	if f.nonceExpiry != nil {
		if expiry, ok := f.nonceExpiry[nonce]; ok && f.now.Before(expiry) {
			return nil
		}
		f.nonceExpiry[nonce] = f.now.Add(f.nonceTTL)
	}
	f.created = append(f.created, call)
	return nil
}

type fakeNotificationProgress struct {
	delivered    map[string]scheduler.NotificationFragment
	markCalls    []scheduler.NotificationFragment
	failMarkOnce bool
}

func (f *fakeNotificationProgress) DeliveredNotificationFragments(context.Context, string, string) ([]scheduler.NotificationFragment, error) {
	result := make([]scheduler.NotificationFragment, 0, len(f.delivered))
	for _, fragment := range f.delivered {
		result = append(result, fragment)
	}
	return result, nil
}

func (f *fakeNotificationProgress) MarkNotificationFragmentDelivered(_ context.Context, _, _ string, fragment scheduler.NotificationFragment) error {
	f.markCalls = append(f.markCalls, fragment)
	if f.failMarkOnce {
		f.failMarkOnce = false
		return errors.New("injected local mark failure")
	}
	if f.delivered == nil {
		f.delivered = make(map[string]scheduler.NotificationFragment)
	}
	f.delivered[fmt.Sprintf("%s/%d", fragment.Route, fragment.FragmentIndex)] = fragment
	return nil
}

func vacancyNotificationEvent(channel string) scheduler.Event {
	return scheduler.Event{
		Kind: "vacancy",
		Job: scheduler.Job{
			ID:            "42",
			Account:       "user",
			Channel:       channel,
			MateriaCodigo: "3.1.050",
			Label:         "Fisica II",
		},
		Outcome: scheduler.Outcome{Code: "found", Vacancies: []scheduler.Vacancy{{
			Materia: "Física II",
			Turno:   "Noche",
			Sede:    "Monserrat",
			Horario: "18:30 22:00",
			Dias:    []string{"LU", "MI"},
			Cupos:   3,
		}}},
	}
}

func assertVacancyNotification(t *testing.T, send discordSend) {
	t.Helper()
	for _, want := range []string{"Fisica II", "3.1.050", "Física II", "Noche", "Monserrat", "18:30 22:00", "LU, MI", "3 cupos"} {
		if !strings.Contains(send.content, want) {
			t.Errorf("notification missing %q: %q", want, send.content)
		}
	}
	if got := len(send.components); got != 1 {
		t.Fatalf("components = %d, want 1", got)
	}
	row, ok := send.components[0].(discord.ActionRowComponent)
	if !ok {
		t.Fatalf("component type = %T, want discord.ActionRowComponent", send.components[0])
	}
	buttons := row.Buttons()
	if len(buttons) != 1 {
		t.Fatalf("buttons = %d, want 1", len(buttons))
	}
	button := buttons[0]
	if button.CustomID != "detener_job:42" || button.Label != "Detener busqueda" || button.Style != discord.ButtonStyleDanger {
		t.Fatalf("button = %+v, want detener_job:42 / Detener busqueda / Danger", button)
	}
}

func TestNotificationMessagesLongMultibyteReconstructExactly(t *testing.T) {
	event := vacancyNotificationEvent("channel")
	event.Outcome.Vacancies = make([]scheduler.Vacancy, 12)
	for i := range event.Outcome.Vacancies {
		repetitions := 42
		if i == 0 {
			repetitions = 400
		}
		repeated := strings.Repeat(fmt.Sprintf("á界-%02d-", i), repetitions)
		event.Outcome.Vacancies[i] = scheduler.Vacancy{
			Materia: fmt.Sprintf("Materia única %02d %s", i, repeated),
			Turno:   fmt.Sprintf("Turno único %02d", i),
			Sede:    fmt.Sprintf("Sede única %02d %s", i, repeated),
			Horario: fmt.Sprintf("Horario único %02d %s", i, repeated),
			Dias:    []string{fmt.Sprintf("Día único %02d A", i), fmt.Sprintf("Día único %02d B", i)},
			Cupos:   i + 1,
		}
	}

	dm := notificationMessages(event, notificationRouteDM)
	channel := notificationMessages(event, notificationRouteChannel)
	if len(dm) < 3 || len(channel) < 3 {
		t.Fatalf("fragment counts dm/channel = %d/%d, want at least 3 each", len(dm), len(channel))
	}
	for route, messages := range map[notificationRoute][]notificationMessage{notificationRouteDM: dm, notificationRouteChannel: channel} {
		for i, message := range messages {
			if got := utf8.RuneCountInString(message.Content); got > discordContentLimit {
				t.Fatalf("route %s fragment %d has %d runes, limit %d", route, i, got, discordContentLimit)
			}
			wantComponents := 0
			if i == len(messages)-1 {
				wantComponents = 1
			}
			if len(message.Components) != wantComponents {
				t.Fatalf("route %s fragment %d components = %d, want %d", route, i, len(message.Components), wantComponents)
			}
		}
		decoded, err := decodeNotificationMessages(messages, event.Job.Account)
		if err != nil {
			t.Fatalf("decode route %s: %v", route, err)
		}
		if decoded != notificationText(event) {
			t.Fatalf("route %s did not reconstruct canonical payload", route)
		}
		if got := parseNotificationVacancies(t, decoded); !reflect.DeepEqual(got, event.Outcome.Vacancies) {
			t.Fatalf("route %s vacancies mismatch\ngot:  %#v\nwant: %#v", route, got, event.Outcome.Vacancies)
		}
	}

	mention := "<@" + event.Job.Account + ">\n"
	if !strings.HasPrefix(channel[0].Content, mention) {
		t.Fatalf("first channel fragment lacks owner mention prefix: %q", channel[0].Content)
	}
	for i, message := range channel[1:] {
		if strings.Contains(message.Content, mention) {
			t.Fatalf("channel fragment %d repeats owner mention", i+1)
		}
	}
	for i, message := range dm {
		if strings.Contains(message.Content, "<@"+event.Job.Account+">") {
			t.Fatalf("dm fragment %d contains owner mention", i)
		}
	}
	longField := escapeDiscordText(event.Outcome.Vacancies[0].Materia)
	for i, message := range dm {
		if strings.Contains(message.Content, longField) {
			t.Fatalf("fixture did not split the first long field; it is whole in fragment %d", i)
		}
	}
}

func decodeNotificationMessages(messages []notificationMessage, owner string) (string, error) {
	var decoded strings.Builder
	for i, message := range messages {
		content := message.Content
		if i == 0 {
			content = strings.TrimPrefix(content, "<@"+owner+">\n")
		}
		marker := fmt.Sprintf("⟦parte %d/%d⟧\n", i+1, len(messages))
		if !strings.HasPrefix(content, marker) {
			return "", fmt.Errorf("fragment %d marker mismatch", i)
		}
		decoded.WriteString(strings.TrimPrefix(content, marker))
	}
	return decoded.String(), nil
}

func parseNotificationVacancies(t *testing.T, payload string) []scheduler.Vacancy {
	t.Helper()
	const firstField = "**Materia:** "
	start := strings.Index(payload, firstField)
	if start < 0 {
		t.Fatal("canonical payload has no vacancy blocks")
	}
	blocks := strings.Split(payload[start:], "\n\n")
	vacancies := make([]scheduler.Vacancy, 0, len(blocks))
	for _, block := range blocks {
		lines := strings.Split(block, "\n")
		if len(lines) != 6 {
			t.Fatalf("vacancy block has %d lines: %q", len(lines), block)
		}
		cupos, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(lines[5], "**"), " cupos**"))
		if err != nil {
			t.Fatalf("parse cupos: %v", err)
		}
		vacancies = append(vacancies, scheduler.Vacancy{
			Materia: strings.TrimPrefix(lines[0], "**Materia:** "),
			Turno:   strings.TrimPrefix(lines[1], "**Turno:** "),
			Sede:    strings.TrimPrefix(lines[2], "**Sede:** "),
			Horario: strings.TrimPrefix(lines[3], "**Horario:** "),
			Dias:    strings.Split(strings.TrimPrefix(lines[4], "**Dias:** "), ", "),
			Cupos:   cupos,
		})
	}
	return vacancies
}

func TestOutboundNotifierRoutesAreIndependentAndRetryOnlyPending(t *testing.T) {
	for _, tc := range []struct {
		name           string
		dmFailures     map[int]error
		channelFailure map[int]error
		wantDMCalls    int
		wantChanCalls  int
	}{
		{name: "dm fails channel completes", dmFailures: map[int]error{0: errors.New("dm failed")}, wantDMCalls: 2, wantChanCalls: 1},
		{name: "channel fails dm completes", channelFailure: map[int]error{0: errors.New("channel failed")}, wantDMCalls: 1, wantChanCalls: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeDiscordSender{dmFailures: tc.dmFailures, channelFailures: tc.channelFailure}
			progress := &fakeNotificationProgress{}
			event := vacancyNotificationEvent("channel")
			event.DeliveryKey = strings.Repeat("a", 64)
			n := outboundNotifier{discord: fake, progress: progress}

			if err := n.Notify(context.Background(), event); err == nil {
				t.Fatal("first notify error = nil, want route failure")
			}
			if err := n.Notify(context.Background(), event); err != nil {
				t.Fatalf("retry: %v", err)
			}
			if len(fake.dmCalls) != tc.wantDMCalls || len(fake.channelCalls) != tc.wantChanCalls {
				t.Fatalf("calls dm/channel = %d/%d, want %d/%d", len(fake.dmCalls), len(fake.channelCalls), tc.wantDMCalls, tc.wantChanCalls)
			}
			if len(progress.delivered) != 2 {
				t.Fatalf("durable fragments = %d, want one per route", len(progress.delivered))
			}
		})
	}
}

func TestOutboundNotifierRetryPreservesCompletedFragments(t *testing.T) {
	event := vacancyNotificationEvent("channel")
	event.DeliveryKey = strings.Repeat("b", 64)
	event.Outcome.Vacancies[0].Materia = strings.Repeat("á界", 2500)
	fake := &fakeDiscordSender{dmFailures: map[int]error{1: errors.New("middle fragment failed")}}
	progress := &fakeNotificationProgress{}
	n := outboundNotifier{discord: fake, progress: progress}

	if err := n.Notify(context.Background(), event); err == nil {
		t.Fatal("first notify error = nil")
	}
	dmCount := len(notificationMessages(event, notificationRouteDM))
	if len(fake.dmCalls) != 2 {
		t.Fatalf("first attempt dm calls = %d, want stop at second fragment", len(fake.dmCalls))
	}
	if err := n.Notify(context.Background(), event); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if got := len(fake.dmCalls); got != dmCount+1 {
		t.Fatalf("total dm calls = %d, want failed fragment plus later pending only (%d)", got, dmCount+1)
	}
	if fake.dmCalls[0].nonce == fake.dmCalls[2].nonce {
		t.Fatal("retry resent the already-confirmed first fragment")
	}
}

func TestOutboundNotifierNonceIdentityIsStableAndScoped(t *testing.T) {
	event := vacancyNotificationEvent("")
	event.DeliveryKey = strings.Repeat("d", 64)
	components := []discord.ContainerComponent{discordrest.VacancyActionRow(event.Job.ID)}
	base := notificationFragmentNonce(event, notificationRouteDM, "target", 0, 2, "content", components)
	if len(base) != 25 || !strings.HasPrefix(base, "uade-") {
		t.Fatalf("nonce = %q, want uade- plus 20 hex", base)
	}
	if base != notificationFragmentNonce(event, notificationRouteDM, "target", 0, 2, "content", components) {
		t.Fatal("identical fragment produced a different nonce")
	}
	otherDelivery := event
	otherDelivery.DeliveryKey = strings.Repeat("e", 64)
	otherJob := event
	otherJob.Job.ID = "84"
	variants := []string{
		notificationFragmentNonce(event, notificationRouteChannel, "target", 0, 2, "content", components),
		notificationFragmentNonce(event, notificationRouteDM, "other", 0, 2, "content", components),
		notificationFragmentNonce(event, notificationRouteDM, "target", 1, 2, "content", components),
		notificationFragmentNonce(event, notificationRouteDM, "target", 0, 3, "content", components),
		notificationFragmentNonce(event, notificationRouteDM, "target", 0, 2, "changed", components),
		notificationFragmentNonce(otherDelivery, notificationRouteDM, "target", 0, 2, "content", components),
		notificationFragmentNonce(otherJob, notificationRouteDM, "target", 0, 2, "content", components),
		notificationFragmentNonce(event, notificationRouteDM, "target", 0, 2, "content", []discord.ContainerComponent{discordrest.VacancyActionRow("84")}),
	}
	for i, variant := range variants {
		if variant == base {
			t.Fatalf("identity variant %d reused base nonce", i)
		}
	}
}

func TestOutboundNotifierNonceSeparatesDeliveriesAndRetries(t *testing.T) {
	first := vacancyNotificationEvent("channel")
	first.DeliveryKey = strings.Repeat("a", 64)
	second := first
	second.Job.ID = "84"
	second.DeliveryKey = strings.Repeat("b", 64)

	firstMessages := notificationMessages(first, notificationRouteDM)
	secondMessages := notificationMessages(second, notificationRouteDM)
	retryMessages := notificationMessages(first, notificationRouteDM)
	if len(firstMessages) != len(secondMessages) || len(firstMessages) != len(retryMessages) {
		t.Fatalf("message counts = %d/%d/%d", len(firstMessages), len(secondMessages), len(retryMessages))
	}
	for i := range firstMessages {
		if firstMessages[i].Content != secondMessages[i].Content || firstMessages[i].FragmentIndex != secondMessages[i].FragmentIndex || firstMessages[i].FragmentCount != secondMessages[i].FragmentCount {
			t.Fatalf("fragment %d did not preserve the adversarial visible identity", i)
		}
		if reflect.DeepEqual(firstMessages[i].Components, secondMessages[i].Components) {
			t.Fatalf("fragment %d components unexpectedly equal for different action rows", i)
		}
		if firstMessages[i].Nonce == secondMessages[i].Nonce {
			t.Fatalf("fragment %d reused nonce %q across jobs/deliveries/action rows", i, firstMessages[i].Nonce)
		}
		if firstMessages[i].Nonce != retryMessages[i].Nonce {
			t.Fatalf("fragment %d retry nonce = %q, want %q", i, retryMessages[i].Nonce, firstMessages[i].Nonce)
		}
		if len(firstMessages[i].Nonce) > 25 || !strings.HasPrefix(firstMessages[i].Nonce, "uade-") {
			t.Fatalf("fragment %d nonce = %q, want at most 25 chars with uade- prefix", i, firstMessages[i].Nonce)
		}
	}
}

func TestOutboundNotifierAccountPauseWithoutDurableIdentityDoesNotUseNonce(t *testing.T) {
	fake := &fakeDiscordSender{}
	event := scheduler.Event{
		Kind:   "account_pause",
		Job:    scheduler.Job{ID: "42", Account: "user"},
		Reason: "needs_credentials",
	}
	if err := (outboundNotifier{discord: fake}).Notify(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if len(fake.dmCalls) != 1 {
		t.Fatalf("DM calls = %d, want 1", len(fake.dmCalls))
	}
	if fake.dmCalls[0].nonce != "" {
		t.Fatalf("account-pause nonce = %q, want empty without durable episode identity", fake.dmCalls[0].nonce)
	}
}

func TestOutboundNotifierCrashWindowIsAtLeastOnce(t *testing.T) {
	for _, tc := range []struct {
		name        string
		advance     time.Duration
		wantCreated int
	}{
		{name: "within nonce window deduplicates best effort", advance: time.Minute, wantCreated: 1},
		{name: "after nonce window may duplicate", advance: 11 * time.Minute, wantCreated: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Unix(1000, 0)
			fake := &fakeDiscordSender{now: now, nonceTTL: 10 * time.Minute, nonceExpiry: make(map[string]time.Time)}
			progress := &fakeNotificationProgress{failMarkOnce: true}
			event := vacancyNotificationEvent("")
			event.DeliveryKey = strings.Repeat("c", 64)
			n := outboundNotifier{discord: fake, progress: progress}

			if err := n.Notify(context.Background(), event); err == nil {
				t.Fatal("REST-success/local-mark-failure returned nil")
			}
			fake.now = now.Add(tc.advance)
			if err := n.Notify(context.Background(), event); err != nil {
				t.Fatalf("retry: %v", err)
			}
			if len(fake.dmCalls) != 2 || fake.dmCalls[0].nonce != fake.dmCalls[1].nonce {
				t.Fatalf("retry calls/nonces = %d/%q/%q", len(fake.dmCalls), fake.dmCalls[0].nonce, fake.dmCalls[1].nonce)
			}
			if len(fake.created) != tc.wantCreated {
				t.Fatalf("messages created = %d, want %d", len(fake.created), tc.wantCreated)
			}
		})
	}
}

func TestOutboundNotifierVacancyToChannelIncludesActionRow(t *testing.T) {
	fake := &fakeDiscordSender{}
	n := outboundNotifier{discord: fake}
	event := vacancyNotificationEvent("channel")

	if err := n.Notify(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if len(fake.channelCalls) != 1 || len(fake.dmCalls) != 1 {
		t.Fatalf("channel calls = %d, dm calls = %d", len(fake.channelCalls), len(fake.dmCalls))
	}
	assertVacancyNotification(t, fake.channelCalls[0])
	assertVacancyNotification(t, fake.dmCalls[0])
}

func TestOutboundNotifierVacancyToDMIncludesActionRow(t *testing.T) {
	fake := &fakeDiscordSender{}
	n := outboundNotifier{discord: fake}
	event := vacancyNotificationEvent("")

	if err := n.Notify(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if len(fake.dmCalls) != 1 || len(fake.channelCalls) != 0 {
		t.Fatalf("dm calls = %d, channel calls = %d", len(fake.dmCalls), len(fake.channelCalls))
	}
	assertVacancyNotification(t, fake.dmCalls[0])
}

func TestOutboundNotifierFallsBackToMateriaCode(t *testing.T) {
	event := vacancyNotificationEvent("")
	event.Job.Label = ""
	event.Outcome.Vacancies[0].Materia = ""
	text := notificationText(event)
	if !strings.Contains(text, "3.1.050") {
		t.Fatalf("notification does not fall back to materia code: %q", text)
	}
	if strings.Contains(text, " -  - ") {
		t.Fatalf("notification contains empty identity segments: %q", text)
	}
}

func TestOutboundNotifierEscapesDiscordMentionsInUntrustedFields(t *testing.T) {
	fake := &fakeDiscordSender{}
	event := vacancyNotificationEvent("")
	event.Job.Label = "Fisica @everyone **urgente**"
	event.Outcome.Vacancies[0] = scheduler.Vacancy{
		Materia: "Física @here <@123456789>",
		Turno:   "Noche @everyone",
		Sede:    "Monserrat @here",
		Horario: "18:30 <@&987654321>",
		Dias:    []string{"LU @everyone", "MI @here"},
		Cupos:   3,
	}
	if err := (outboundNotifier{discord: fake}).Notify(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if len(fake.dmCalls) != 1 || len(fake.dmCalls[0].components) != 1 {
		t.Fatalf("dm calls/components = %d/%d, want 1/1", len(fake.dmCalls), len(fake.dmCalls[0].components))
	}
	content := fake.dmCalls[0].content
	for _, executable := range []string{"@everyone", "@here", "<@123456789>", "<@&987654321>"} {
		if strings.Contains(content, executable) {
			t.Errorf("notification contains executable mention %q: %q", executable, content)
		}
	}
	for _, readable := range []string{"Fisica", "Física", "Noche", "Monserrat", "18:30", "LU", "MI"} {
		if !strings.Contains(content, readable) {
			t.Errorf("escaped notification lost readable value %q: %q", readable, content)
		}
	}
}

func TestOutboundNotifierAccountPauseHasNoComponents(t *testing.T) {
	fake := &fakeDiscordSender{}
	n := outboundNotifier{discord: fake}
	event := scheduler.Event{Kind: "account_pause", Job: scheduler.Job{ID: "42", Account: "user"}, Reason: "needs_credentials"}

	if err := n.Notify(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if len(fake.dmCalls) != 1 || len(fake.channelCalls) != 0 {
		t.Fatalf("dm calls = %d, channel calls = %d", len(fake.dmCalls), len(fake.channelCalls))
	}
	if got := len(fake.dmCalls[0].components); got != 0 {
		t.Fatalf("components = %d, want 0", got)
	}
}

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

func TestRuntimeJobHydratesNotificationIdentityByIDAndOwner(t *testing.T) {
	db := newTestRuntimeDB(t)
	seedRuntimeAccount(t, db, "owner", "secret-user", "secret-password", "https://inscripcionespia.uade.edu.ar/InscripcionClaseBuscar.aspx?param=secret")
	seedRuntimeAccount(t, db, "other", "other-user", "other-password", "https://inscripcionespia.uade.edu.ar/InscripcionClaseBuscar.aspx?param=other")
	jobID := seedRuntimeJob(t, db, "owner", `{"materiaCodigo":"3.1.050","turno":"Noche","secret":"must-not-flow"}`)
	if _, err := db.Exec(`UPDATE jobs SET label='Fisica II' WHERE id=?`, jobID); err != nil {
		t.Fatal(err)
	}

	runtime := &Runtime{DB: db}
	job, err := runtime.job(scheduler.PersistedJob{ID: jobID, Account: "owner", Channel: "channel"})
	if err != nil {
		t.Fatal(err)
	}
	if job.MateriaCodigo != "3.1.050" || job.Label != "Fisica II" {
		t.Fatalf("identity = %q/%q, want 3.1.050/Fisica II", job.MateriaCodigo, job.Label)
	}
	if _, err = runtime.job(scheduler.PersistedJob{ID: jobID, Account: "other"}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("wrong-owner error = %v, want sql.ErrNoRows", err)
	}

	serialized := fmt.Sprintf("%+v", job)
	for _, secret := range []string{"must-not-flow", "secret-user", "secret-password", "param=secret"} {
		if strings.Contains(serialized, secret) {
			t.Errorf("scheduler.Job leaked %q: %s", secret, serialized)
		}
	}
}

func TestRuntimeJobFallsBackToCodeAndRejectsInvalidFilters(t *testing.T) {
	db := newTestRuntimeDB(t)
	seedRuntimeAccount(t, db, "owner", "u", "p", testHealedStartURL)
	jobID := seedRuntimeJob(t, db, "owner", `{"materiaCodigo":"3.1.050"}`)
	runtime := &Runtime{DB: db}

	job, err := runtime.job(scheduler.PersistedJob{ID: jobID, Account: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	if job.Label != "3.1.050" {
		t.Fatalf("empty label fallback = %q, want 3.1.050", job.Label)
	}
	if _, err = db.Exec(`UPDATE jobs SET filtros_json='{' WHERE id=?`, jobID); err != nil {
		t.Fatal(err)
	}
	if _, err = runtime.job(scheduler.PersistedJob{ID: jobID, Account: "owner"}); err == nil {
		t.Fatal("invalid filtros_json unexpectedly accepted")
	}
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
func TestPollPreservesMateriaAfterHealingStaleStartURL(t *testing.T) {
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
	if _, err := db.Exec(`UPDATE jobs SET label='Mi etiqueta personalizada' WHERE id=?`, jobID); err != nil {
		t.Fatal(err)
	}

	runtime, err := NewRuntime(context.Background(), db, testMasterKey, "", "https://inscripciones.uade.edu.ar/", time.Minute, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	runtime.relink = func(context.Context, *http.Client, string, string, string) (sso.Result, error) {
		return sso.Result{StartURL: testHealedStartURL}, nil
	}

	job, err := runtime.job(scheduler.PersistedJob{ID: jobID, Account: account})
	if err != nil {
		t.Fatal(err)
	}
	if job.MateriaCodigo != "3.1.050" || job.Label != "Mi etiqueta personalizada" {
		t.Fatalf("job identity=%q/%q, want code and custom label kept separate", job.MateriaCodigo, job.Label)
	}
	outcome, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("poll returned unexpected error: %v", err)
	}
	if outcome.Code == "stale_start_url" {
		t.Fatalf("expected healed poll to not fall back to stale_start_url, got %+v", outcome)
	}
	if outcome.Code != "found" {
		t.Fatalf("expected the healed link to complete the search with a found outcome, got %+v", outcome)
	}
	if len(outcome.Vacancies) != 1 || !strings.Contains(outcome.Vacancies[0].Materia, "Física") {
		t.Fatalf("poll did not preserve UADE materia name: %+v", outcome.Vacancies)
	}
	if outcome.MateriaCodigo != "3.1.050" || outcome.MateriaNombre != "Física II" {
		t.Fatalf("outcome identity=%q/%q, want code and academic name kept separate", outcome.MateriaCodigo, outcome.MateriaNombre)
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
