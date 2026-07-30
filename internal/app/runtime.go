package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/disgoorg/disgo/discord"
	credentialcrypto "github.com/ogs/uade-bot/internal/crypto"
	"github.com/ogs/uade-bot/internal/discordrest"
	"github.com/ogs/uade-bot/internal/scheduler"
	"github.com/ogs/uade-bot/internal/sso"
	"github.com/ogs/uade-bot/internal/uade"
)

type Runtime struct {
	DB           *sql.DB
	MasterKey    string
	SSOPortalURL string
	Scheduler    *scheduler.Scheduler
	ctx          context.Context
	cancel       context.CancelFunc
	interval     time.Duration
	// relink is injectable so tests can substitute a fake without hitting a
	// real UADE/Microsoft host; production wiring defaults it to sso.Relink.
	relink func(ctx context.Context, client *http.Client, portalURL, user, password string) (sso.Result, error)
}

type filters struct {
	MateriaCodigo  string   `json:"materiaCodigo"`
	Ofrecimiento   string   `json:"ofrecimiento"`
	Turno          string   `json:"turno"`
	Dias           []string `json:"dias"`
	SedesExcluidas []string `json:"sedesExcluidas"`
}

type discordSender interface {
	SendDM(user, content, nonce string, components ...discord.ContainerComponent) error
	SendMention(channel, owner, content, nonce string, components ...discord.ContainerComponent) error
}

type notificationProgressStore interface {
	DeliveredNotificationFragments(context.Context, string, string) ([]scheduler.NotificationFragment, error)
	MarkNotificationFragmentDelivered(context.Context, string, string, scheduler.NotificationFragment) error
}

type outboundNotifier struct {
	discord  discordSender
	progress notificationProgressStore
}

const discordContentLimit = 2000

type notificationRoute string

const (
	notificationRouteDM      notificationRoute = "dm"
	notificationRouteChannel notificationRoute = "channel"
)

type notificationMessage struct {
	Content             string
	Components          []discord.ContainerComponent
	FragmentIndex       int
	FragmentCount       int
	FragmentFingerprint string
	Nonce               string
}

func (n outboundNotifier) Notify(ctx context.Context, event scheduler.Event) error {
	if event.Kind == "vacancy" {
		if n.progress != nil && event.DeliveryKey == "" {
			return errors.New("notification delivery key is required")
		}
		delivered := make(map[string]scheduler.NotificationFragment)
		if n.progress != nil {
			fragments, err := n.progress.DeliveredNotificationFragments(ctx, event.Job.ID, event.DeliveryKey)
			if err != nil {
				log.Printf("notification progress unavailable job=%s", event.Job.ID)
				return errors.New("notification progress unavailable")
			}
			for _, fragment := range fragments {
				delivered[notificationFragmentKey(notificationRoute(fragment.Route), fragment.FragmentIndex)] = fragment
			}
		}

		routes := []notificationRoute{notificationRouteDM}
		if event.Job.Channel != "" {
			routes = append(routes, notificationRouteChannel)
		}
		var routeErrors []error
		for _, route := range routes {
			messages := notificationMessages(event, route)
			for _, message := range messages {
				key := notificationFragmentKey(route, message.FragmentIndex)
				if previous, ok := delivered[key]; ok {
					if previous.FragmentCount != message.FragmentCount || previous.FragmentFingerprint != message.FragmentFingerprint {
						log.Printf("notification delivery conflict job=%s route=%s", event.Job.ID, route)
						routeErrors = append(routeErrors, fmt.Errorf("notification delivery conflict route=%s", route))
						break
					}
					continue
				}
				var sendErr error
				if route == notificationRouteChannel {
					sendErr = n.discord.SendMention(event.Job.Channel, event.Job.Account, message.Content, message.Nonce, message.Components...)
				} else {
					sendErr = n.discord.SendDM(event.Job.Account, message.Content, message.Nonce, message.Components...)
				}
				if sendErr != nil {
					log.Printf("notification delivery failed job=%s route=%s", event.Job.ID, route)
					routeErrors = append(routeErrors, fmt.Errorf("notification delivery failed route=%s", route))
					break
				}
				fragment := scheduler.NotificationFragment{
					Route:               string(route),
					FragmentIndex:       message.FragmentIndex,
					FragmentCount:       message.FragmentCount,
					FragmentFingerprint: message.FragmentFingerprint,
					DeliveredAt:         time.Now().UTC(),
				}
				if n.progress != nil {
					if err := n.progress.MarkNotificationFragmentDelivered(ctx, event.Job.ID, event.DeliveryKey, fragment); err != nil {
						log.Printf("notification progress mark failed job=%s route=%s", event.Job.ID, route)
						routeErrors = append(routeErrors, fmt.Errorf("notification progress mark failed route=%s", route))
						break
					}
				}
				delivered[key] = fragment
			}
		}
		return errors.Join(routeErrors...)
	}
	content := notificationText(event)
	nonce := notificationFragmentNonce(notificationRouteDM, event.Job.Account, 0, 1, content)
	return n.discord.SendDM(event.Job.Account, content, nonce)
}

func notificationMessages(event scheduler.Event, route notificationRoute) []notificationMessage {
	mention := ""
	if route == notificationRouteChannel {
		mention = "<@" + event.Job.Account + ">\n"
	}
	contents := splitNotificationContent(notificationText(event), mention)
	messages := make([]notificationMessage, len(contents))
	for i, content := range contents {
		target := event.Job.Account
		if route == notificationRouteChannel {
			target = event.Job.Channel
		}
		fingerprint := sha256.Sum256([]byte(content))
		messages[i] = notificationMessage{
			Content:             content,
			FragmentIndex:       i,
			FragmentCount:       len(contents),
			FragmentFingerprint: hex.EncodeToString(fingerprint[:]),
			Nonce:               notificationFragmentNonce(route, target, i, len(contents), content),
		}
		if i == len(contents)-1 {
			messages[i].Components = []discord.ContainerComponent{discordrest.VacancyActionRow(event.Job.ID)}
		}
	}
	return messages
}

func notificationFragmentKey(route notificationRoute, index int) string {
	return fmt.Sprintf("%s/%d", route, index)
}

func notificationFragmentNonce(route notificationRoute, target string, index, count int, content string) string {
	hash := sha256.New()
	writeNotificationIdentityString(hash, string(route))
	writeNotificationIdentityString(hash, target)
	var number [8]byte
	binary.BigEndian.PutUint64(number[:], uint64(index))
	hash.Write(number[:])
	binary.BigEndian.PutUint64(number[:], uint64(count))
	hash.Write(number[:])
	writeNotificationIdentityString(hash, content)
	return "uade-" + hex.EncodeToString(hash.Sum(nil))[:20]
}

type notificationIdentityWriter interface {
	Write([]byte) (int, error)
}

func writeNotificationIdentityString(writer notificationIdentityWriter, value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = writer.Write(length[:])
	_, _ = writer.Write([]byte(value))
}

func splitNotificationContent(payload, firstPrefix string) []string {
	runes := []rune(payload)
	for expectedCount := 1; ; {
		contents := make([]string, 0, expectedCount)
		position := 0
		for index := 1; position < len(runes) || (len(runes) == 0 && index == 1); index++ {
			prefix := fmt.Sprintf("⟦parte %d/%d⟧\n", index, expectedCount)
			if index == 1 {
				prefix = firstPrefix + prefix
			}
			capacity := discordContentLimit - utf8.RuneCountInString(prefix)
			if capacity <= 0 {
				panic("notification fragment prefix exceeds Discord content limit")
			}
			end := position + capacity
			if end > len(runes) {
				end = len(runes)
			}
			contents = append(contents, prefix+string(runes[position:end]))
			position = end
		}
		if len(contents) == expectedCount {
			return contents
		}
		expectedCount = len(contents)
	}
}

func notificationText(event scheduler.Event) string {
	if event.Kind == "account_pause" {
		switch event.Reason {
		case "needs_credentials":
			return "Pausé tus búsquedas: UADE rechazó las credenciales. Actualizalas con /credenciales."
		case "needs_new_start_url":
			return "Pausé tus búsquedas: no pude renovar automáticamente tu link de inscripción (puede requerir verificación adicional de tu cuenta). Volvé a correr /credenciales para reintentarlo."
		default:
			return "Pausé temporalmente tus búsquedas de UADE."
		}
	}
	identity := notificationIdentity(event.Job, event.Outcome)
	lines := []string{fmt.Sprintf("Se encontro una vacante para **%s**.", identity)}
	for _, vacancy := range event.Outcome.Vacancies {
		lines = append(lines, "")
		days := make([]string, 0, len(vacancy.Dias))
		for _, day := range vacancy.Dias {
			days = append(days, escapeDiscordText(day))
		}
		lines = append(lines,
			fmt.Sprintf("**Materia:** %s", escapeDiscordText(vacancy.Materia)),
			fmt.Sprintf("**Turno:** %s", escapeDiscordText(vacancy.Turno)),
			fmt.Sprintf("**Sede:** %s", escapeDiscordText(vacancy.Sede)),
			fmt.Sprintf("**Horario:** %s", escapeDiscordText(vacancy.Horario)),
			fmt.Sprintf("**Dias:** %s", strings.Join(days, ", ")),
			fmt.Sprintf("**%d cupos**", vacancy.Cupos),
		)
	}
	return strings.Join(lines, "\n")
}

func notificationIdentity(job scheduler.Job, outcome scheduler.Outcome) string {
	code := escapeDiscordText(job.MateriaCodigo)
	materia := code
	for _, vacancy := range outcome.Vacancies {
		if name := strings.TrimSpace(vacancy.Materia); name != "" {
			materia += " - " + escapeDiscordText(name)
			break
		}
	}
	label := strings.TrimSpace(job.Label)
	if label == "" || label == job.MateriaCodigo {
		return materia
	}
	return escapeDiscordText(label) + " - " + materia
}

// escapeDiscordText keeps user/UADE-controlled values readable while making
// Discord mentions and Markdown delimiters inert. Structural Markdown in the
// formatter itself is intentionally added only after each value is escaped.
func escapeDiscordText(value string) string {
	replacer := strings.NewReplacer(
		`\`, `\\`,
		`*`, `\*`,
		`_`, `\_`,
		`~`, `\~`,
		"`", "\\`",
		`|`, `\|`,
		`>`, `\>`,
		`[`, `\[`,
		`]`, `\]`,
		"@", "@\u200b",
	)
	return replacer.Replace(value)
}

func NewRuntime(parent context.Context, db *sql.DB, masterKey, discordToken, ssoPortalURL string, interval time.Duration, concurrency int, shadow bool) (*Runtime, error) {
	if db == nil || masterKey == "" {
		return nil, errors.New("runtime requires database and credentials master key")
	}
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ctx, cancel := context.WithCancel(parent)
	store := scheduler.SQLStore{DB: db}
	options := []scheduler.Option{scheduler.WithStore(store), scheduler.WithShadow(shadow)}
	if discordToken != "" && !shadow {
		options = append(options, scheduler.WithNotifier(&scheduler.ThrottledNotifier{Next: outboundNotifier{discord: discordrest.New(discordToken), progress: store}, Delay: 250 * time.Millisecond}))
	}
	runtime := &Runtime{DB: db, MasterKey: masterKey, SSOPortalURL: ssoPortalURL, Scheduler: scheduler.New(concurrency, options...), ctx: ctx, cancel: cancel, interval: interval, relink: sso.Relink}
	if _, err := runtime.Scheduler.Reconcile(ctx, runtime.job); err != nil {
		cancel()
		return nil, err
	}
	return runtime, nil
}

// recoverGoroutine converts a panic in the current goroutine into a log line
// instead of letting it crash the process. Callers defer it as the first
// statement of a goroutine body (or, for Start's ticker loop, of safeTick --
// scoped to one tick, never the whole outer goroutine, so a recovered panic
// does not silently stop all future ticks). See CR-01 in 03.3-REVIEW.md.
//
// The value returned by recover() is used only to decide whether a panic
// occurred; it is never formatted, wrapped, returned, or logged. A panic
// can originate from dependencies handling UADE credentials, Discord
// tokens, or start URLs, so the recovered value itself must never reach a
// log sink (D-06/GO-02, T-03.3-13-01). Only a fixed marker plus the
// caller's static stage name is logged.
func recoverGoroutine(name string) {
	if recover() != nil {
		log.Printf("runtime goroutine panic recovered stage=%s", name)
	}
}

// safeTick runs one scheduler tick (reconcile + RunOnce). It is extracted out
// of Start's ticker case so a panic here is recovered without ever stopping
// the ticker's outer for/select loop -- the next tick still fires.
func (r *Runtime) safeTick() {
	defer recoverGoroutine("runtime.tick")
	if _, err := r.Scheduler.Reconcile(r.ctx, r.job); err != nil {
		log.Printf("scheduler reconcile failed: %v", err)
		return
	}
	if err := r.Scheduler.RunOnce(r.ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("scheduler tick failed: %v", err)
	}
}

func (r *Runtime) Start() {
	go func() {
		ticker := time.NewTicker(r.interval)
		defer ticker.Stop()
		for {
			select {
			case <-r.ctx.Done():
				return
			case <-ticker.C:
				r.safeTick()
			}
		}
	}()
}

func (r *Runtime) Close() {
	r.cancel()
	r.Scheduler.Wait()
}

func (r *Runtime) JobCreated(id string) {
	go func() {
		defer recoverGoroutine("runtime.JobCreated")
		if _, err := r.Scheduler.Reconcile(r.ctx, r.job); err != nil {
			log.Printf("scheduler reconcile after job creation failed: %v", err)
			return
		}
		r.Scheduler.PollNow(r.ctx, id)
	}()
}

func (r *Runtime) AccountReady(account string) {
	go func() {
		defer recoverGoroutine("runtime.AccountReady")
		r.attemptRelinkOnAccountReady(r.ctx, account)
		if _, err := r.Scheduler.Reconcile(r.ctx, r.job); err != nil {
			log.Printf("scheduler reconcile after credential update failed: %v", err)
			return
		}
		r.pollAllActiveJobs(r.ctx, account)
	}()
}

// pollAllActiveJobs triggers an immediate poll for every active job of
// account. Factored out of AccountReady so it can be reused unchanged after
// attemptRelinkOnAccountReady runs first.
func (r *Runtime) pollAllActiveJobs(ctx context.Context, account string) {
	rows, err := r.DB.QueryContext(ctx, `SELECT id FROM jobs WHERE discord_user_id=? AND status='active' ORDER BY id`, account)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			r.Scheduler.PollNow(ctx, id)
		}
	}
}

// attemptRelinkOnAccountReady is a best-effort relink triggered right after
// credentials are saved/rotated via the normal credentials modal. It always
// runs inside the goroutine AccountReady already launches with
// go func(){...}(), fully decoupled from the 2400ms interactionDeadline
// enforced in internal/discordgateway/listeners.go for the Discord
// interaction that triggered submitCredentials (see the response-deadline
// race documented across 03.3-08/03.3-10). This is exactly why sso.Relink is
// never called synchronously inside submitCredentials: an HTTP relink
// against UADE/Microsoft can take several seconds, well past the
// interaction's response deadline. If no credentials row exists yet, or
// decryption fails, this is a silent no-op -- the first real poll's own
// healStartURL call will retry.
func (r *Runtime) attemptRelinkOnAccountReady(ctx context.Context, account string) {
	var encrypted credentialcrypto.Ciphertext
	err := r.DB.QueryRowContext(ctx, `SELECT ciphertext,iv,auth_tag FROM credentials WHERE discord_user_id=?`, account).Scan(&encrypted.Ciphertext, &encrypted.IV, &encrypted.AuthTag)
	if err != nil {
		return
	}
	credentials, err := credentialcrypto.Decrypt(r.MasterKey, account, encrypted)
	if err != nil {
		return
	}
	_, _ = r.healStartURL(ctx, account, credentials)
}

func (r *Runtime) JobsChanged() {
	go func() {
		defer recoverGoroutine("runtime.JobsChanged")
		if _, err := r.Scheduler.Reconcile(r.ctx, r.job); err != nil {
			log.Printf("scheduler reconcile after command failed: %v", err)
		}
	}()
}

func (r *Runtime) job(record scheduler.PersistedJob) (scheduler.Job, error) {
	var rawFilters, label string
	if err := r.DB.QueryRowContext(context.Background(), `SELECT filtros_json, COALESCE(label, '') FROM jobs WHERE id=? AND discord_user_id=?`, record.ID, record.Account).Scan(&rawFilters, &label); err != nil {
		return scheduler.Job{}, err
	}
	var selected filters
	if err := json.Unmarshal([]byte(rawFilters), &selected); err != nil {
		return scheduler.Job{}, errors.New("invalid stored filters")
	}
	label = strings.TrimSpace(label)
	if label == "" {
		label = selected.MateriaCodigo
	}
	return scheduler.Job{Account: record.Account, ID: record.ID, Channel: record.Channel, MateriaCodigo: selected.MateriaCodigo, Label: label, Run: func(ctx context.Context) (scheduler.Outcome, error) {
		return r.poll(ctx, record.ID, record.Account)
	}}, nil
}

func (r *Runtime) poll(ctx context.Context, jobID, account string) (scheduler.Outcome, error) {
	var rawFilters string
	var encrypted credentialcrypto.Ciphertext
	err := r.DB.QueryRowContext(ctx, `SELECT j.filtros_json,c.ciphertext,c.iv,c.auth_tag FROM jobs j JOIN credentials c ON c.discord_user_id=j.discord_user_id WHERE j.id=? AND j.discord_user_id=? AND j.status='active'`, jobID, account).Scan(&rawFilters, &encrypted.Ciphertext, &encrypted.IV, &encrypted.AuthTag)
	if err != nil {
		return scheduler.Outcome{}, err
	}
	var selected filters
	if err = json.Unmarshal([]byte(rawFilters), &selected); err != nil {
		return scheduler.Outcome{}, errors.New("invalid stored filters")
	}
	credentials, err := credentialcrypto.Decrypt(r.MasterKey, account, encrypted)
	if err != nil {
		return scheduler.Outcome{Code: "invalid_credentials"}, nil
	}
	start, err := parseStartURL(credentials.UADEStartURL)
	if err != nil {
		healed, healErr := r.healStartURL(ctx, account, credentials)
		if healErr != nil {
			// Covers every healStartURL failure mode, including
			// sso.ErrMFARequired -- markNeedsManualStartURL (called inside
			// healStartURL) already recorded the manual-action state; the
			// scheduler's existing stale_start_url handling still owns
			// pausing + notifying the account.
			return scheduler.Outcome{Code: "stale_start_url"}, nil
		}
		credentials.UADEStartURL = healed
		start, err = parseStartURL(credentials.UADEStartURL)
		if err != nil {
			return scheduler.Outcome{Code: "stale_start_url"}, nil
		}
	}
	client, err := uade.NewClient(start.Scheme + "://" + start.Host)
	if err != nil {
		return scheduler.Outcome{}, err
	}
	outcome := client.Search(ctx, credentials.UADEStartURL, credentials.UADEUsername, credentials.UADEPassword, uade.SearchFilters{MateriaCodigo: selected.MateriaCodigo, Ofrecimiento: selected.Ofrecimiento, Turno: selected.Turno, Dias: selected.Dias}, selected.SedesExcluidas)
	converted := scheduler.Outcome{Code: string(outcome.Code)}
	for _, vacancy := range outcome.Vacancies {
		converted.Vacancies = append(converted.Vacancies, scheduler.Vacancy{Materia: vacancy.Materia, Turno: vacancy.Turno, Sede: vacancy.Sede, Horario: vacancy.Horario, Dias: strings.Split(vacancy.Dias, ","), Cupos: vacancy.Cupos})
	}
	return converted, nil
}

// parseStartURL validates raw against the same rules sso.Relink's own output
// must satisfy (sso.ValidStartURL: https, exact enrollment host, has a
// param= query key) and only then parses it. A single source of truth for
// "is this start URL usable" avoids poll() and healStartURL disagreeing
// about what counts as stale.
func parseStartURL(raw string) (*url.URL, error) {
	if !sso.ValidStartURL(raw) {
		return nil, errors.New("invalid or stale uade start url")
	}
	return url.Parse(raw)
}

// healStartURL attempts an SSO relink for account and, on success, persists
// the fresh start URL (encrypted) before returning it. On an
// sso.ErrMFARequired failure it best-effort marks the account
// needs_new_start_url so the pause DM and /credenciales both reflect the
// real blocker instead of a generic staleness code; the markNeedsManualStartURL
// error itself is intentionally swallowed so it never shadows the original
// relink error returned to the caller.
func (r *Runtime) healStartURL(ctx context.Context, account string, credentials credentialcrypto.Credentials) (string, error) {
	client, err := sso.NewClient(r.SSOPortalURL)
	if err != nil {
		return "", err
	}
	result, err := r.relink(ctx, client, r.SSOPortalURL, credentials.UADEUsername, credentials.UADEPassword)
	if err != nil {
		if errors.Is(err, sso.ErrMFARequired) {
			_ = r.markNeedsManualStartURL(ctx, account)
		}
		return "", err
	}
	credentials.UADEStartURL = result.StartURL
	if err = r.persistStartURL(ctx, account, credentials); err != nil {
		return "", err
	}
	return result.StartURL, nil
}

// persistStartURL re-encrypts credentials (with its freshly-healed start
// URL) and updates the stored row. Neither the Encrypt result nor
// credentials itself is ever logged.
func (r *Runtime) persistStartURL(ctx context.Context, account string, credentials credentialcrypto.Credentials) error {
	encrypted, err := credentialcrypto.Encrypt(r.MasterKey, account, credentials)
	if err != nil {
		return err
	}
	_, err = r.DB.ExecContext(ctx, `UPDATE credentials SET ciphertext=?,iv=?,auth_tag=?,updated_at=? WHERE discord_user_id=?`, encrypted.Ciphertext, encrypted.IV, encrypted.AuthTag, time.Now().UnixMilli(), account)
	return err
}

// markNeedsManualStartURL records that account's relink hit
// sso.ErrMFARequired, reusing the exact same pause_reason mechanism the
// scheduler already uses when poll() detects staleness -- same consumer in
// the pause DM (notificationText) and in /credenciales (03.3-16) -- while
// preserving any existing BackoffAttempt/LastPauseNotifiedReason instead of
// clobbering them.
func (r *Runtime) markNeedsManualStartURL(ctx context.Context, account string) error {
	repo := scheduler.SQLStore{DB: r.DB}
	current, err := repo.AccountState(ctx, account)
	if err != nil {
		return err
	}
	current.Reason = "needs_new_start_url"
	return repo.SaveAccountState(ctx, account, current)
}
