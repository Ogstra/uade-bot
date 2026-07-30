package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

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
	SendDM(user, content string, components ...discord.ContainerComponent) error
	Send(channel, content string, components ...discord.ContainerComponent) error
}

type outboundNotifier struct{ discord discordSender }

func (n outboundNotifier) Notify(_ context.Context, event scheduler.Event) error {
	content := notificationText(event)
	if event.Kind == "vacancy" {
		row := discordrest.VacancyActionRow(event.Job.ID)
		if event.Job.Channel != "" {
			return n.discord.Send(event.Job.Channel, content, row)
		}
		return n.discord.SendDM(event.Job.Account, content, row)
	}
	return n.discord.SendDM(event.Job.Account, content)
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
	var lines []string
	for _, vacancy := range event.Outcome.Vacancies {
		lines = append(lines, fmt.Sprintf("%s · %s · %s · %d cupos", vacancy.Turno, vacancy.Sede, vacancy.Horario, vacancy.Cupos))
	}
	return "¡Hay vacantes para tu búsqueda!\n" + strings.Join(lines, "\n")
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
		options = append(options, scheduler.WithNotifier(&scheduler.ThrottledNotifier{Next: outboundNotifier{discord: discordrest.New(discordToken)}, Delay: 250 * time.Millisecond}))
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
	return scheduler.Job{Account: record.Account, ID: record.ID, Channel: record.Channel, Run: func(ctx context.Context) (scheduler.Outcome, error) {
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
		converted.Vacancies = append(converted.Vacancies, scheduler.Vacancy{Turno: vacancy.Turno, Sede: vacancy.Sede, Horario: vacancy.Horario, Dias: strings.Split(vacancy.Dias, ","), Cupos: vacancy.Cupos})
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
