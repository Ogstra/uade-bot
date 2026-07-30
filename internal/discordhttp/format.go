package discordhttp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// jobFilters mirrors the filtros_json shape already written by buscar()
// (materiaCodigo/turno/ofrecimiento/dias/sedesExcluidas). Deliberately
// duplicated here rather than shared with internal/app's own unexported
// filters type or internal/dashboard's Filters/safeFilters -- same pattern
// already established twice in the repo, not a new duplication.
type jobFilters struct {
	MateriaCodigo  string
	Turno          string
	Ofrecimiento   string
	Dias           []string
	SedesExcluidas []string
}

// parseJobFilters decodes filtros_json. Invalid or empty JSON produces the
// zero-value struct, never an error or panic -- callers always get
// something displayable.
func parseJobFilters(raw string) jobFilters {
	var x struct {
		MateriaCodigo  string   `json:"materiaCodigo"`
		Turno          string   `json:"turno"`
		Ofrecimiento   string   `json:"ofrecimiento"`
		Dias           []string `json:"dias"`
		SedesExcluidas []string `json:"sedesExcluidas"`
	}
	_ = json.Unmarshal([]byte(raw), &x)
	return jobFilters{
		MateriaCodigo:  x.MateriaCodigo,
		Turno:          x.Turno,
		Ofrecimiento:   x.Ofrecimiento,
		Dias:           x.Dias,
		SedesExcluidas: x.SedesExcluidas,
	}
}

// lookupMateriaNombre resolves the cached display name of a materia code,
// same pattern as internal/dashboard/snapshot.go's materiaName -- but
// without its 160-rune cap, which exists there only because that value gets
// serialized into the dashboard's JSON payload; nothing analogous applies
// to a Discord command response built directly.
func lookupMateriaNombre(ctx context.Context, db *sql.DB, codigo string) string {
	var name string
	err := db.QueryRowContext(ctx, `SELECT nombre FROM materias WHERE codigo=?`, codigo).Scan(&name)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(name)
}

// formatJobChoiceLabel builds the human-readable detail text for a job's
// autocomplete choice (materia + turno/dias, falling back to the job's own
// label, then to a fixed placeholder). Pure helper -- never receives nor
// returns the job's numeric id; prefixing "#{id}" is the caller's job
// (commands.go's autocomplete()), not this function's, since only the admin
// path needs that prefix.
func formatJobChoiceLabel(label string, filters jobFilters, materiaNombre string) string {
	materia := filters.MateriaCodigo
	if materiaNombre != "" {
		if materia != "" {
			materia += " - " + materiaNombre
		} else {
			materia = materiaNombre
		}
	}
	turnoDias := strings.TrimSpace(filters.Turno + " " + strings.Join(filters.Dias, "/"))

	var detail string
	switch {
	case materia != "" && turnoDias != "":
		detail = materia + " · " + turnoDias
	case materia != "":
		detail = materia
	case turnoDias != "":
		detail = turnoDias
	}

	if detail == "" {
		if label != "" {
			return label
		}
		return "sin datos"
	}
	if label != "" {
		return label + ": " + detail
	}
	return detail
}

// formatJobStatusText translates a job's raw status plus the owning
// account's pause_reason into the same "activa"/"pausada (motivo)" copy
// Node's formatJobStatus produced: a job paused by the user OR an account
// paused for any reason both read as paused.
func formatJobStatusText(jobStatus string, accountPauseReason sql.NullString) string {
	reason := ""
	if accountPauseReason.Valid {
		reason = accountPauseReason.String
	}
	if jobStatus == "paused_by_user" {
		if reason != "" {
			return fmt.Sprintf("pausada (%s)", reason)
		}
		return "pausada"
	}
	if reason != "" {
		return fmt.Sprintf("pausada (%s)", reason)
	}
	return "activa"
}

// formatJobLocation resuelve dónde vive una búsqueda: sin guildID -> "DM" si
// hay channelID, si no "sin canal"; con guildID y channelID -> mención de
// canal (Discord la resuelve del lado cliente); con guildID sin channelID ->
// "servidor {guildID}". A diferencia de Node (que resuelve el nombre del
// guild vía su cache de Gateway), Go no tiene ninguna cache de guilds
// cableada deliberadamente (internal/discordgateway/gateway.go solo pide
// gateway.IntentGuilds, sin cache.FlagRoles/cache.FlagMembers) -- la mención
// de canal ya resuelve visualmente el servidor en el cliente de Discord sin
// necesitar esa cache.
func formatJobLocation(guildID, channelID sql.NullString) string {
	hasGuild := guildID.Valid && guildID.String != ""
	hasChannel := channelID.Valid && channelID.String != ""
	if !hasGuild {
		if hasChannel {
			return "DM"
		}
		return "sin canal"
	}
	if hasChannel {
		return fmt.Sprintf("<#%s>", channelID.String)
	}
	return fmt.Sprintf("servidor %s", guildID.String)
}

// formatLastPoll renders last_polled_at using the process's own local time
// zone, same as Node's Intl.DateTimeFormat call with no explicit timeZone.
func formatLastPoll(ms sql.NullInt64) string {
	if !ms.Valid {
		return "sin sondeos"
	}
	return time.UnixMilli(ms.Int64).Format("02/01/2006 15:04:05")
}

// lastOutcomeLine translates a job's last_outcome code (plus, for "found",
// the most recent poll_outcome_history row) into the same copy Node's
// formatLastOutcome produced. Go's scheduler.Outcome only carries
// Code/Vacancies -- unlike Node, there is no free-text failure reason to
// append for search_failed.
func lastOutcomeLine(ctx context.Context, db *sql.DB, jobID int64, code string) string {
	switch code {
	case "":
		return "sin resultado"
	case "no_vacancies":
		return "sin vacantes"
	case "invalid_credentials":
		return "credenciales invalidas"
	case "search_failed":
		return "fallo la busqueda"
	case "found":
		var vacancyCount, totalCupos sql.NullInt64
		err := db.QueryRowContext(ctx, `SELECT vacancy_count, total_cupos FROM poll_outcome_history WHERE job_id=? ORDER BY recorded_at DESC, id DESC LIMIT 1`, jobID).Scan(&vacancyCount, &totalCupos)
		if err != nil {
			return "vacantes encontradas"
		}
		if vacancyCount.Valid && vacancyCount.Int64 == 1 {
			return fmt.Sprintf("vacante encontrada (%d cupos)", totalCupos.Int64)
		}
		return fmt.Sprintf("vacantes encontradas (%d cursos, %d cupos)", vacancyCount.Int64, totalCupos.Int64)
	default:
		return code
	}
}

// truncateAdminList joins lines and, if the result would exceed Discord's
// ~2000-char message cap, cuts it down to ~1850 chars plus a suffix stating
// how many lines were omitted -- same behavior as Node's adminJobListMessage
// (messages.js L299-312).
func truncateAdminList(lines []string) string {
	joined := strings.Join(lines, "\n")
	if len(joined) <= 1900 {
		return joined
	}
	var truncated strings.Builder
	shown := 0
	for _, line := range lines {
		extra := len(line) + 1
		if truncated.Len()+extra > 1850 {
			break
		}
		if truncated.Len() > 0 {
			truncated.WriteString("\n")
		}
		truncated.WriteString(line)
		shown++
	}
	return fmt.Sprintf("%s\n_...y %d mas (no entran en un solo mensaje)._", truncated.String(), len(lines)-shown)
}

// validDias is the fixed whitelist of day codes /buscar accepts, mirroring
// Node's VALID_DIAS (src/discord/commands/buscar.js L19).
var validDias = map[string]bool{"LU": true, "MA": true, "MI": true, "JU": true, "VI": true, "SA": true}

// filterValidDias reuses csvValues' upper+trim+non-empty normalization, then
// silently drops any entry outside validDias -- same as Node's
// `.filter(dia => VALID_DIAS.has(dia))`, not a per-entry error.
func filterValidDias(value string) []string {
	candidates := csvValues(value, true)
	out := make([]string, 0, len(candidates))
	for _, dia := range candidates {
		if validDias[dia] {
			out = append(out, dia)
		}
	}
	return out
}

// filterSummaryBlock arma el mismo resumen de filtros que Node's
// searchCreatedMessage produce como `base` (messages.js L57-64), sin el
// sufijo de pausa/credenciales que arma el caller aparte.
func filterSummaryBlock(label, materiaCodigo, materiaNombre, turno, ofrecimiento string, dias, sedes []string) string {
	block := fmt.Sprintf("**Busqueda creada:** %s\n**Materia:** `%s`", label, materiaCodigo)
	if materiaNombre != "" {
		block += fmt.Sprintf(" - %s", materiaNombre)
	}
	block += fmt.Sprintf("\n**Turno:** %s\n**Ofrecimiento:** %s\n**Dias:** %s", turno, ofrecimiento, strings.Join(dias, ", "))
	if len(sedes) > 0 {
		block += fmt.Sprintf("\n**Sedes excluidas:** %s", strings.Join(sedes, ", "))
	}
	return block
}
