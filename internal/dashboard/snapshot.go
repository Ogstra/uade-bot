package dashboard

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

type SnapshotFunc func() (Snapshot, error)

type Snapshot struct {
	GeneratedAt int64     `json:"generatedAt"`
	BotGuilds   []Guild   `json:"botGuilds"`
	Health      Health    `json:"health"`
	Accounts    []Account `json:"accounts"`
}
type Guild struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type Status struct {
	Code  any    `json:"code"`
	Label string `json:"label"`
	Tone  string `json:"tone"`
}
type Outcome struct {
	Code         any    `json:"code"`
	Label        string `json:"label"`
	Tone         string `json:"tone"`
	VacancyCount *int   `json:"vacancyCount"`
	TotalCupos   *int   `json:"totalCupos"`
}
type Filters struct {
	MateriaCodigo       string   `json:"materiaCodigo"`
	MateriaNombre       *string  `json:"materiaNombre"`
	Ofrecimiento        string   `json:"ofrecimiento"`
	Turno               string   `json:"turno"`
	Dias                []string `json:"dias"`
	SedesExcluidas      []string `json:"sedesExcluidas"`
	SedesExcluidasLabel string   `json:"sedesExcluidasLabel"`
}
type HistoryItem struct {
	ID         int64   `json:"id"`
	RecordedAt int64   `json:"recordedAt"`
	Outcome    Outcome `json:"outcome"`
}
type Job struct {
	JobID        int64         `json:"jobId"`
	Label        string        `json:"label"`
	Status       Status        `json:"status"`
	LastPolledAt *int64        `json:"lastPolledAt"`
	Outcome      Outcome       `json:"outcome"`
	Filters      Filters       `json:"filters"`
	History      []HistoryItem `json:"history"`
}
type Account struct {
	DiscordUserID string `json:"discordUserId"`
	DisplayName   string `json:"displayName"`
	Status        Status `json:"status"`
	PauseUntil    *int64 `json:"pauseUntil"`
	JobCount      int    `json:"jobCount"`
	LastPolledAt  *int64 `json:"lastPolledAt"`
	Jobs          []Job  `json:"jobs"`
}
type Breakdown struct {
	Code  string `json:"code"`
	Label string `json:"label"`
	Count int    `json:"count"`
}
type PausedHealth struct {
	Total     int         `json:"total"`
	Breakdown []Breakdown `json:"breakdown"`
}
type JobsHealth struct {
	Total          int `json:"total"`
	Active         int `json:"active"`
	ManuallyPaused int `json:"manuallyPaused"`
}
type Health struct {
	ActiveAccounts       int          `json:"activeAccounts"`
	PausedAccounts       PausedHealth `json:"pausedAccounts"`
	Jobs                 JobsHealth   `json:"jobs"`
	LastSuccessfulPollAt *int64       `json:"lastSuccessfulPollAt"`
}

type SnapshotSource struct {
	DB           *sql.DB
	Now          func() time.Time
	DisplayNames map[string]string
	Guilds       []Guild
}

type rawJob struct {
	id                                    int64
	user, label, status, filters, outcome string
	last                                  sql.NullInt64
}
type rawPause struct {
	reason string
	until  sql.NullInt64
}

func (s SnapshotSource) Build() (Snapshot, error) {
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	out := Snapshot{GeneratedAt: now.UnixMilli(), BotGuilds: append([]Guild{}, s.Guilds...), Accounts: []Account{}, Health: Health{PausedAccounts: PausedHealth{Breakdown: []Breakdown{}}, Jobs: JobsHealth{}}}
	if s.DB == nil {
		return out, nil
	}
	pauses := map[string]rawPause{}
	rows, err := s.DB.Query(`SELECT discord_user_id, pause_reason, pause_until FROM users WHERE pause_reason IS NOT NULL ORDER BY discord_user_id`)
	if err != nil {
		return out, fmt.Errorf("dashboard pauses: %w", err)
	}
	breakdowns := map[string]int{}
	for rows.Next() {
		var id, reason string
		var until sql.NullInt64
		if err = rows.Scan(&id, &reason, &until); err != nil {
			rows.Close()
			return out, err
		}
		pauses[id] = rawPause{reason, until}
		breakdowns[safePause(reason).Code]++
	}
	if err = rows.Close(); err != nil {
		return out, err
	}
	for code, count := range breakdowns {
		p := safePause(code)
		out.Health.PausedAccounts.Breakdown = append(out.Health.PausedAccounts.Breakdown, Breakdown{p.Code, p.Breakdown, count})
	}
	sort.Slice(out.Health.PausedAccounts.Breakdown, func(i, j int) bool {
		return out.Health.PausedAccounts.Breakdown[i].Code < out.Health.PausedAccounts.Breakdown[j].Code
	})
	out.Health.PausedAccounts.Total = len(pauses)
	jobRows, err := s.DB.Query(`SELECT id, discord_user_id, COALESCE(label,''), status, filtros_json, last_polled_at, COALESCE(last_outcome,'') FROM jobs ORDER BY id`)
	if err != nil {
		return out, fmt.Errorf("dashboard jobs: %w", err)
	}
	// Fully drain and close jobRows before running any nested queries
	// (materiaName/loadHistory below). s.DB's pool is pinned to a single
	// connection (store.Open -- see .planning/debug/resolved/nested-sqlite-tx-scheduler.md);
	// holding jobRows open while issuing further queries on the same
	// *sql.DB would block forever waiting for a second connection that
	// can never be granted.
	var jobs []rawJob
	for jobRows.Next() {
		var r rawJob
		if err = jobRows.Scan(&r.id, &r.user, &r.label, &r.status, &r.filters, &r.last, &r.outcome); err != nil {
			jobRows.Close()
			return out, err
		}
		jobs = append(jobs, r)
	}
	if err = jobRows.Err(); err != nil {
		jobRows.Close()
		return out, err
	}
	if err = jobRows.Close(); err != nil {
		return out, err
	}
	grouped := map[string]*Account{}
	for _, r := range jobs {
		out.Health.Jobs.Total++
		if r.status == "active" {
			out.Health.Jobs.Active++
		}
		if r.status == "paused_by_user" {
			out.Health.Jobs.ManuallyPaused++
		}
		pause, paused := pauses[r.user]
		a := grouped[r.user]
		if a == nil {
			name := s.DisplayNames[r.user]
			if name == "" {
				name = "Usuario " + r.user
			}
			a = &Account{DiscordUserID: r.user, DisplayName: name, Status: accountStatus(func() string {
				if paused {
					return pause.reason
				}
				return ""
			}()), Jobs: []Job{}}
			if pause.until.Valid {
				v := pause.until.Int64
				a.PauseUntil = &v
			}
			grouped[r.user] = a
			if !paused {
				out.Health.ActiveAccounts++
			}
		}
		if r.last.Valid {
			v := r.last.Int64
			a.LastPolledAt = maxPtr(a.LastPolledAt, v)
		}
		filters := safeFilters(r.filters)
		if name, err := materiaName(s.DB, filters.MateriaCodigo); err != nil {
			return out, err
		} else {
			filters.MateriaNombre = name
		}
		history, err := loadHistory(s.DB, r.id)
		if err != nil {
			return out, err
		}
		current := safeOutcome(r.outcome)
		if r.last.Valid && (current.Code == "found" || current.Code == "no_vacancies") {
			out.Health.LastSuccessfulPollAt = maxPtr(out.Health.LastSuccessfulPollAt, r.last.Int64)
		}
		for _, h := range history {
			if h.Outcome.Code == "found" || h.Outcome.Code == "no_vacancies" {
				out.Health.LastSuccessfulPollAt = maxPtr(out.Health.LastSuccessfulPollAt, h.RecordedAt)
			}
		}
		label := r.label
		if label == "" {
			label = filters.MateriaCodigo
		}
		j := Job{JobID: r.id, Label: label, Status: jobStatus(r.status, func() string {
			if paused {
				return pause.reason
			}
			return ""
		}()), Outcome: current, Filters: filters, History: history}
		if r.last.Valid {
			v := r.last.Int64
			j.LastPolledAt = &v
		}
		a.Jobs = append(a.Jobs, j)
		a.JobCount++
	}
	ids := make([]string, 0, len(grouped))
	for id := range grouped {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		out.Accounts = append(out.Accounts, *grouped[id])
	}
	sort.Slice(out.BotGuilds, func(i, j int) bool {
		if out.BotGuilds[i].Name == out.BotGuilds[j].Name {
			return out.BotGuilds[i].ID < out.BotGuilds[j].ID
		}
		return out.BotGuilds[i].Name < out.BotGuilds[j].Name
	})
	return out, nil
}

type pauseCopy struct{ Code, Account, Breakdown string }

func safePause(v string) pauseCopy {
	switch v {
	case "needs_credentials":
		return pauseCopy{v, "Pausada: requiere credenciales", "Requiere credenciales"}
	case "needs_new_start_url":
		return pauseCopy{v, "Pausada: requiere un nuevo link", "Requiere un nuevo link"}
	case "rate_limited":
		return pauseCopy{v, "Pausada temporalmente por límite de UADE", "Límite de UADE"}
	default:
		return pauseCopy{"unknown", "Pausada: revisar el estado", "Revisar el estado"}
	}
}
func accountStatus(p string) Status {
	if p == "" {
		return Status{"active", "Activa", "healthy"}
	}
	x := safePause(p)
	return Status{x.Code, x.Account, "warning"}
}
func jobStatus(s, p string) Status {
	if p != "" {
		return accountStatus(p)
	}
	if s == "active" {
		return Status{s, "Activa", "healthy"}
	}
	if s == "paused_by_user" {
		return Status{s, "Pausada manualmente", "warning"}
	}
	return Status{"unknown", "Revisar el estado", "warning"}
}
func unknownOutcome() Outcome {
	return Outcome{"unknown", "Resultado no reconocido", "warning", nil, nil}
}
func safeOutcome(raw string) Outcome {
	if raw == "" {
		return Outcome{nil, "Sin sondeos todavía", "neutral", nil, nil}
	}
	var x struct {
		Outcome   string `json:"outcome"`
		Vacancies []struct {
			Cupos int `json:"cupos"`
		} `json:"vacancies"`
	}
	if json.Unmarshal([]byte(raw), &x) != nil {
		return unknownOutcome()
	}
	if x.Outcome == "found" || x.Outcome == "vacancies_found" {
		if len(x.Vacancies) == 0 {
			return unknownOutcome()
		}
		total := 0
		for _, v := range x.Vacancies {
			if v.Cupos < 0 {
				return unknownOutcome()
			}
			total += v.Cupos
		}
		count := len(x.Vacancies)
		return Outcome{"found", "Vacantes encontradas", "healthy", &count, &total}
	}
	return outcomeCode(x.Outcome, nil, nil)
}
func outcomeCode(code string, count, total *int) Outcome {
	switch code {
	case "no_vacancies":
		return Outcome{code, "Sin vacantes", "neutral", count, total}
	case "search_failed":
		return Outcome{code, "Búsqueda fallida", "failure", count, total}
	case "invalid_credentials":
		return Outcome{code, "Credenciales inválidas", "failure", count, total}
	case "rate_limited":
		return Outcome{code, "Limitada por UADE", "warning", count, total}
	case "stale_start_url", "needs_new_start_url":
		return Outcome{code, "Link de inscripción vencido", "warning", count, total}
	case "found":
		return Outcome{code, "Vacantes encontradas", "healthy", count, total}
	default:
		return unknownOutcome()
	}
}
func safeFilters(raw string) Filters {
	var x struct {
		MateriaCodigo string   `json:"materiaCodigo"`
		Ofrecimiento  string   `json:"ofrecimiento"`
		Turno         string   `json:"turno"`
		Dias          []string `json:"dias"`
		Sedes         []string `json:"sedesExcluidas"`
	}
	_ = json.Unmarshal([]byte(raw), &x)
	if x.MateriaCodigo == "" {
		x.MateriaCodigo = "No disponible"
	}
	if x.Turno == "" {
		x.Turno = "No disponible"
	}
	off := "Curricular"
	if x.Ofrecimiento == "optativa" {
		off = "Optativa"
	}
	if x.Dias == nil {
		x.Dias = []string{}
	}
	if x.Sedes == nil {
		x.Sedes = []string{}
	}
	label := "Sin exclusiones"
	if len(x.Sedes) > 0 {
		label = strings.Join(x.Sedes, ", ")
	}
	return Filters{x.MateriaCodigo, nil, off, x.Turno, x.Dias, x.Sedes, label}
}
func materiaName(db *sql.DB, code string) (*string, error) {
	var name string
	err := db.QueryRow(`SELECT nombre FROM materias WHERE codigo = ?`, code).Scan(&name)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil
	}
	r := []rune(name)
	if len(r) > 160 {
		name = string(r[:160])
	}
	return &name, nil
}
func loadHistory(db *sql.DB, job int64) ([]HistoryItem, error) {
	rows, err := db.Query(`SELECT id, recorded_at, outcome_code, vacancy_count, total_cupos FROM poll_outcome_history WHERE job_id = ? ORDER BY recorded_at DESC, id DESC LIMIT 10`, job)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HistoryItem{}
	for rows.Next() {
		var id, at int64
		var code string
		var c, t sql.NullInt64
		if err = rows.Scan(&id, &at, &code, &c, &t); err != nil {
			return nil, err
		}
		var cp, tp *int
		if c.Valid {
			v := int(c.Int64)
			cp = &v
		}
		if t.Valid {
			v := int(t.Int64)
			tp = &v
		}
		out = append(out, HistoryItem{id, at, outcomeCode(code, cp, tp)})
	}
	return out, rows.Err()
}
func maxPtr(current *int64, v int64) *int64 {
	if current == nil || v > *current {
		x := v
		return &x
	}
	return current
}
