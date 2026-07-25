package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	credentialcrypto "github.com/ogs/uade-bot/internal/crypto"
	"github.com/ogs/uade-bot/internal/discordrest"
	"github.com/ogs/uade-bot/internal/scheduler"
	"github.com/ogs/uade-bot/internal/uade"
)

type Runtime struct {
	DB        *sql.DB
	MasterKey string
	Scheduler *scheduler.Scheduler
	ctx       context.Context
	cancel    context.CancelFunc
	interval  time.Duration
}

type filters struct {
	MateriaCodigo  string   `json:"materiaCodigo"`
	Ofrecimiento   string   `json:"ofrecimiento"`
	Turno          string   `json:"turno"`
	Dias           []string `json:"dias"`
	SedesExcluidas []string `json:"sedesExcluidas"`
}

type outboundNotifier struct{ discord discordrest.Notifier }

func (n outboundNotifier) Notify(_ context.Context, event scheduler.Event) error {
	content := notificationText(event)
	if event.Kind == "vacancy" && event.Job.Channel != "" {
		return n.discord.Send(event.Job.Channel, content)
	}
	return n.discord.SendDM(event.Job.Account, content)
}

func notificationText(event scheduler.Event) string {
	if event.Kind == "account_pause" {
		switch event.Reason {
		case "needs_credentials":
			return "Pausé tus búsquedas: UADE rechazó las credenciales. Actualizalas con /credenciales."
		case "needs_new_start_url":
			return "Pausé tus búsquedas: el link de inscripción venció. Actualizalo con /credenciales modo:Link de inscripción."
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

func NewRuntime(parent context.Context, db *sql.DB, masterKey, discordToken string, interval time.Duration, concurrency int, shadow bool) (*Runtime, error) {
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
	runtime := &Runtime{DB: db, MasterKey: masterKey, Scheduler: scheduler.New(concurrency, options...), ctx: ctx, cancel: cancel, interval: interval}
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
		if _, err := r.Scheduler.Reconcile(r.ctx, r.job); err != nil {
			log.Printf("scheduler reconcile after credential update failed: %v", err)
			return
		}
		rows, err := r.DB.QueryContext(r.ctx, `SELECT id FROM jobs WHERE discord_user_id=? AND status='active' ORDER BY id`, account)
		if err != nil {
			return
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil {
				r.Scheduler.PollNow(r.ctx, id)
			}
		}
	}()
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
	start, err := url.Parse(credentials.UADEStartURL)
	if err != nil || start.Scheme != "https" || !strings.EqualFold(start.Hostname(), "inscripcionespia.uade.edu.ar") {
		return scheduler.Outcome{Code: "stale_start_url"}, nil
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
