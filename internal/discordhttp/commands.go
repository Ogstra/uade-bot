package discordhttp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	credentialcrypto "github.com/ogs/uade-bot/internal/crypto"
	"github.com/ogs/uade-bot/internal/sso"
)

const ephemeral = 1 << 6

var materiaPattern = regexp.MustCompile(`^\d+(?:\.\d+){2}$`)

// Interaction is the exported, transport-agnostic contract for a Discord
// interaction. Both the HTTP-webhook JSON wrapper (Dispatch) and
// internal/discordgateway's Gateway adapter construct/populate this same
// shape before calling DispatchInteraction.
type Interaction struct {
	Type      int             `json:"type"`
	GuildID   string          `json:"guild_id"`
	ChannelID string          `json:"channel_id"`
	Data      InteractionData `json:"data"`
	Member    Member          `json:"member"`
	User      User            `json:"user"`
}

// InteractionData mirrors Discord's interaction "data" object across slash
// commands, modal submits and autocomplete requests.
type InteractionData struct {
	Name     string          `json:"name"`
	CustomID string          `json:"custom_id"`
	Options  []Option        `json:"options"`
	Values   json.RawMessage `json:"components"`
}

// Member mirrors Discord's resolved guild-member object attached to an
// interaction; Permissions is the decimal-string bitmask Discord sends
// (see hasAdministrator).
type Member struct {
	Permissions string `json:"permissions"`
	User        User   `json:"user"`
}

// User mirrors Discord's minimal user object.
type User struct {
	ID string `json:"id"`
}

// Option mirrors a single slash-command/autocomplete option value.
type Option struct {
	Name    string `json:"name"`
	Type    int    `json:"type"`
	Value   any    `json:"value"`
	Focused bool   `json:"focused"`
}

// InteractionResponse is the Discord interaction callback payload. Data is
// intentionally opaque so command handlers never need to serialize secrets.
type InteractionResponse struct {
	Type int `json:"type"`
	Data any `json:"data,omitempty"`
}

// CommandDispatcher implements all global slash commands and modal submits.
// It does not accept a guild allow-list: ownership is account based and admin
// authorization comes from Discord's Administrator permission bit.
type CommandDispatcher struct {
	DB             *sql.DB
	MasterKey      string
	OnJobCreated   func(string)
	OnAccountReady func(string)
	OnJobsChanged  func()
}

// Dispatch is a thin JSON-unmarshal wrapper around DispatchInteraction, kept
// for the HTTP-webhook transport's wire format. Gateway ingress
// (internal/discordgateway) constructs an Interaction directly and calls
// DispatchInteraction to avoid a marshal/unmarshal round-trip.
func (d CommandDispatcher) Dispatch(ctx context.Context, body []byte) (InteractionResponse, error) {
	var in Interaction
	if err := json.Unmarshal(body, &in); err != nil {
		return InteractionResponse{}, err
	}
	return d.DispatchInteraction(ctx, in)
}

// DispatchInteraction contains all command/modal/autocomplete business
// logic, independent of transport (HTTP webhook or Gateway).
func (d CommandDispatcher) DispatchInteraction(ctx context.Context, in Interaction) (InteractionResponse, error) {
	userID := in.Member.User.ID
	if userID == "" {
		userID = in.User.ID
	}
	if userID == "" {
		return message("No pude identificar tu cuenta."), nil
	}
	if in.Type == 5 {
		if in.Data.CustomID == "sso_manual_link" {
			return d.submitManualStartURL(ctx, userID, in.Data.Values)
		}
		return d.submitCredentials(ctx, userID, in.Data.CustomID, in.Data.Values)
	}
	if in.Type == 4 {
		return d.autocomplete(ctx, userID, in)
	}
	if in.Type != 2 {
		return InteractionResponse{}, errors.New("unsupported interaction type")
	}
	admin := strings.HasPrefix(in.Data.Name, "admin-")
	if admin && !hasAdministrator(in.Member.Permissions) {
		return message("Este comando requiere el permiso Administrador."), nil
	}
	if d.DB == nil {
		return InteractionResponse{}, errors.New("database unavailable")
	}
	if err := d.logCommand(ctx, userID, in.Data.Name, in.GuildID); err != nil {
		return InteractionResponse{}, err
	}
	switch in.Data.Name {
	case "credenciales":
		return d.credencialesModal(ctx, userID)
	case "buscar":
		return d.buscar(ctx, userID, in.ChannelID, in.GuildID, in.Data.Options)
	case "estado":
		return d.estado(ctx, userID, false, "")
	case "detener", "pausar", "reanudar":
		return d.mutateJob(ctx, userID, in.Data.Name, stringOption(in.Data.Options, "busqueda"), false)
	case "admin-estado":
		return d.estado(ctx, userID, true, stringOption(in.Data.Options, "usuario"))
	case "admin-detener", "admin-pausar", "admin-reanudar":
		action := strings.TrimPrefix(in.Data.Name, "admin-")
		return d.mutateJob(ctx, userID, action, stringOption(in.Data.Options, "busqueda"), true)
	case "admin-stats":
		return d.adminStats(ctx)
	case "admin-user-stats":
		return d.adminUserStats(ctx, stringOption(in.Data.Options, "usuario"))
	default:
		return message("Comando desconocido."), nil
	}
}

func message(content string) InteractionResponse {
	return InteractionResponse{Type: 4, Data: map[string]any{"content": content, "flags": ephemeral}}
}

func credentialsModal() InteractionResponse {
	fields := []any{}
	add := func(id, label string, style int) {
		fields = append(fields, map[string]any{"type": 1, "components": []any{map[string]any{
			"type": 4, "custom_id": id, "label": label, "style": style, "required": true,
		}}})
	}
	add("uade_username", "Usuario UADE", 1)
	add("uade_password", "Password UADE", 1)
	return InteractionResponse{Type: 9, Data: map[string]any{
		"custom_id": "credentials", "title": "Credenciales UADE", "components": fields,
	}}
}

// manualLinkModal is the ephemeral (type 9, D-06) fallback modal used only
// for an account marked needs_new_start_url after the automatic SSO relink
// hit an MFA challenge (03.3-15). It never uses a DM.
func manualLinkModal() InteractionResponse {
	fields := []any{map[string]any{"type": 1, "components": []any{map[string]any{
		"type": 4, "custom_id": "start_url", "label": "Pegá tu link de inscripción", "style": 1, "required": true,
	}}}}
	return InteractionResponse{Type: 9, Data: map[string]any{
		"custom_id": "sso_manual_link", "title": "Link de inscripción UADE", "components": fields,
	}}
}

// credencialesModal picks which ephemeral modal /credenciales opens: the
// normal username/password modal by default, or the manual-link fallback
// only when the account is currently paused because the automatic SSO
// relink hit MFA (pause_reason='needs_new_start_url', set by 03.3-15).
func (d CommandDispatcher) credencialesModal(ctx context.Context, userID string) (InteractionResponse, error) {
	var reason sql.NullString
	err := d.DB.QueryRowContext(ctx, `SELECT pause_reason FROM users WHERE discord_user_id=?`, userID).Scan(&reason)
	if err != nil && err != sql.ErrNoRows {
		return InteractionResponse{}, err
	}
	if reason.String == "needs_new_start_url" {
		return manualLinkModal(), nil
	}
	return credentialsModal(), nil
}

func (d CommandDispatcher) submitCredentials(ctx context.Context, userID, customID string, raw json.RawMessage) (InteractionResponse, error) {
	if customID != "credentials" {
		return message("Formulario desconocido."), nil
	}
	values := modalValues(raw)
	creds, err := d.readCredentials(ctx, userID)
	if err != nil && err != sql.ErrNoRows {
		return InteractionResponse{}, err
	}
	creds.UADEUsername = values["uade_username"]
	creds.UADEPassword = values["uade_password"]
	if creds.UADEUsername == "" || creds.UADEPassword == "" || d.MasterKey == "" {
		return message("Faltan datos para guardar las credenciales."), nil
	}
	encrypted, err := credentialcrypto.Encrypt(d.MasterKey, userID, creds)
	if err != nil {
		return InteractionResponse{}, err
	}
	now := time.Now().UnixMilli()
	tx, err := d.DB.BeginTx(ctx, nil)
	if err != nil {
		return InteractionResponse{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO users(discord_user_id, created_at, updated_at) VALUES(?,?,?) ON CONFLICT(discord_user_id) DO UPDATE SET pause_reason=NULL,pause_until=NULL,backoff_attempt=0,updated_at=excluded.updated_at`, userID, now, now); err != nil {
		return InteractionResponse{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO credentials(discord_user_id,ciphertext,iv,auth_tag,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(discord_user_id) DO UPDATE SET ciphertext=excluded.ciphertext,iv=excluded.iv,auth_tag=excluded.auth_tag,updated_at=excluded.updated_at`, userID, encrypted.Ciphertext, encrypted.IV, encrypted.AuthTag, now); err != nil {
		return InteractionResponse{}, err
	}
	if err = tx.Commit(); err != nil {
		return InteractionResponse{}, err
	}
	if d.OnAccountReady != nil {
		d.OnAccountReady(userID)
	}
	jobID, jobErr := d.materializePendingSearch(ctx, userID)
	if jobErr != nil {
		return InteractionResponse{}, jobErr
	}
	if jobID != "" {
		if d.OnJobCreated != nil {
			d.OnJobCreated(jobID)
		}
		return message("Credenciales guardadas de forma cifrada. Ya creé la búsqueda que habías pedido con /buscar."), nil
	}
	return message("Credenciales guardadas de forma cifrada. Todavía no creé ninguna búsqueda: volvé a usar /buscar para crearla."), nil
}

// savePendingSearch persists the exact filters/channel/guild/label of a
// /buscar request made while the user has no saved credentials yet, so
// submitCredentials can materialize it later instead of losing it. Upserts
// by discord_user_id: a second /buscar before the modal is completed
// overwrites the previous pending row rather than accumulating rows.
func (d CommandDispatcher) savePendingSearch(ctx context.Context, userID, channelID, guildID, label, filtersJSON string) error {
	_, err := d.DB.ExecContext(ctx, `INSERT INTO pending_searches(discord_user_id,filtros_json,channel_id,guild_id,label,created_at) VALUES(?,?,?,?,?,?) ON CONFLICT(discord_user_id) DO UPDATE SET filtros_json=excluded.filtros_json,channel_id=excluded.channel_id,guild_id=excluded.guild_id,label=excluded.label,created_at=excluded.created_at`, userID, filtersJSON, nullable(channelID), nullable(guildID), label, time.Now().UnixMilli())
	return err
}

// materializePendingSearch converts a saved pending_searches row (if any)
// into an active job with the exact filters originally requested, then
// deletes the pending row. Returns "" (no error) when there is nothing
// pending -- that's the normal case, not a failure.
func (d CommandDispatcher) materializePendingSearch(ctx context.Context, userID string) (string, error) {
	var filtersJSON, label string
	var channelID, guildID sql.NullString
	err := d.DB.QueryRowContext(ctx, `SELECT filtros_json,channel_id,guild_id,label FROM pending_searches WHERE discord_user_id=?`, userID).Scan(&filtersJSON, &channelID, &guildID, &label)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	tx, err := d.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	now := time.Now().UnixMilli()
	result, err := tx.ExecContext(ctx, `INSERT INTO jobs(discord_user_id,filtros_json,channel_id,guild_id,label,status,created_at) VALUES(?,?,?,?,?,'active',?)`, userID, filtersJSON, channelID, guildID, label, now)
	if err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM pending_searches WHERE discord_user_id=?`, userID); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(id, 10), nil
}

func (d CommandDispatcher) readCredentials(ctx context.Context, userID string) (credentialcrypto.Credentials, error) {
	var c credentialcrypto.Ciphertext
	err := d.DB.QueryRowContext(ctx, `SELECT ciphertext,iv,auth_tag FROM credentials WHERE discord_user_id=?`, userID).Scan(&c.Ciphertext, &c.IV, &c.AuthTag)
	if err != nil {
		return credentialcrypto.Credentials{}, err
	}
	return credentialcrypto.Decrypt(d.MasterKey, userID, c)
}

// submitManualStartURL validates and persists a manually-pasted enrollment
// link submitted through manualLinkModal, then clears the account's
// needs_new_start_url pause so the next scheduled poll uses the fresh link.
// Deliberately does NOT call OnAccountReady: that would trigger another
// background SSO relink attempt (03.3-15's attemptRelinkOnAccountReady),
// which could immediately re-mark needs_new_start_url and undo the manual
// reactivation the user just performed. OnJobsChanged reconciles the
// scheduler instead.
func (d CommandDispatcher) submitManualStartURL(ctx context.Context, userID string, raw json.RawMessage) (InteractionResponse, error) {
	values := modalValues(raw)
	link := strings.TrimSpace(values["start_url"])
	if !sso.ValidStartURL(link) {
		return message("Ese link no parece válido. Tiene que ser el link completo de inscripcionespia.uade.edu.ar con param=."), nil
	}
	creds, err := d.readCredentials(ctx, userID)
	if err != nil {
		return InteractionResponse{}, err
	}
	creds.UADEStartURL = link
	encrypted, err := credentialcrypto.Encrypt(d.MasterKey, userID, creds)
	if err != nil {
		return InteractionResponse{}, err
	}
	now := time.Now().UnixMilli()
	tx, err := d.DB.BeginTx(ctx, nil)
	if err != nil {
		return InteractionResponse{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE credentials SET ciphertext=?,iv=?,auth_tag=?,updated_at=? WHERE discord_user_id=?`, encrypted.Ciphertext, encrypted.IV, encrypted.AuthTag, now, userID); err != nil {
		return InteractionResponse{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE users SET pause_reason=NULL, pause_until=NULL, backoff_attempt=0, updated_at=? WHERE discord_user_id=?`, now, userID); err != nil {
		return InteractionResponse{}, err
	}
	if err = tx.Commit(); err != nil {
		return InteractionResponse{}, err
	}
	if d.OnJobsChanged != nil {
		d.OnJobsChanged()
	}
	return message("Link guardado. Reactivé tus búsquedas."), nil
}

func modalValues(raw json.RawMessage) map[string]string {
	var rows []struct {
		Components []struct {
			CustomID string `json:"custom_id"`
			Value    string `json:"value"`
		} `json:"components"`
	}
	_ = json.Unmarshal(raw, &rows)
	out := make(map[string]string)
	for _, row := range rows {
		for _, field := range row.Components {
			out[field.CustomID] = strings.TrimSpace(field.Value)
		}
	}
	return out
}

func (d CommandDispatcher) buscar(ctx context.Context, userID, channelID, guildID string, options []Option) (InteractionResponse, error) {
	code := strings.TrimSpace(stringOption(options, "cod_materia"))
	if !materiaPattern.MatchString(code) {
		return message("El código de materia no es válido (ejemplo: 3.1.050)."), nil
	}
	filters, _ := json.Marshal(map[string]any{"materiaCodigo": code, "turno": stringOption(options, "turno"), "ofrecimiento": stringOption(options, "ofrecimiento"), "dias": csvValues(stringOption(options, "dias"), true), "sedesExcluidas": csvValues(stringOption(options, "sedes_excluidas"), false)})
	label := stringOption(options, "etiqueta")
	if label == "" {
		label = code
	}
	var credentials int
	if err := d.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM credentials WHERE discord_user_id=?`, userID).Scan(&credentials); err != nil {
		return InteractionResponse{}, err
	}
	if credentials == 0 {
		// Abrir el modal directamente evita que el usuario tenga que descubrir y
		// tipear /credenciales. La búsqueda pedida se persiste en
		// pending_searches (upsert por discord_user_id) para que
		// submitCredentials pueda materializarla apenas se guarden las
		// credenciales, en vez de perderla.
		if err := d.savePendingSearch(ctx, userID, channelID, guildID, label, string(filters)); err != nil {
			return InteractionResponse{}, err
		}
		return credentialsModal(), nil
	}
	var active int
	if err := d.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE discord_user_id=? AND status IN ('active','paused_by_user')`, userID).Scan(&active); err != nil {
		return InteractionResponse{}, err
	}
	if active >= 10 {
		return message("Ya tenés 10 búsquedas activas o pausadas."), nil
	}
	now := time.Now().UnixMilli()
	result, err := d.DB.ExecContext(ctx, `INSERT INTO jobs(discord_user_id,filtros_json,channel_id,guild_id,label,status,created_at) VALUES(?,?,?,?,?,'active',?)`, userID, string(filters), nullable(channelID), nullable(guildID), label, now)
	if err != nil {
		return InteractionResponse{}, err
	}
	if d.OnJobCreated != nil {
		if id, idErr := result.LastInsertId(); idErr == nil {
			d.OnJobCreated(strconv.FormatInt(id, 10))
		}
	}
	return message("Búsqueda creada. El scheduler hará el primer intento sin bloquear esta interacción."), nil
}

func (d CommandDispatcher) estado(ctx context.Context, userID string, admin bool, filter string) (InteractionResponse, error) {
	query := `SELECT id,discord_user_id,COALESCE(label,''),status FROM jobs`
	args := []any{}
	if !admin {
		query += ` WHERE discord_user_id=?`
		args = append(args, userID)
	} else if filter != "" {
		query += ` WHERE discord_user_id=?`
		args = append(args, filter)
	}
	query += ` ORDER BY id LIMIT 25`
	rows, err := d.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return InteractionResponse{}, err
	}
	defer rows.Close()
	lines := []string{}
	for rows.Next() {
		var id int
		var owner, label, status string
		if err = rows.Scan(&id, &owner, &label, &status); err != nil {
			return InteractionResponse{}, err
		}
		if label == "" {
			label = "sin etiqueta"
		}
		if admin {
			lines = append(lines, fmt.Sprintf("#%d · %s · %s · %s", id, owner, label, status))
		} else {
			lines = append(lines, fmt.Sprintf("#%d · %s · %s", id, label, status))
		}
	}
	if len(lines) == 0 {
		return message("No hay búsquedas para mostrar."), rows.Err()
	}
	return message(strings.Join(lines, "\n")), rows.Err()
}

func (d CommandDispatcher) mutateJob(ctx context.Context, userID, action, idText string, admin bool) (InteractionResponse, error) {
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil {
		return message("Seleccioná una búsqueda válida."), nil
	}
	status := ""
	if action == "pausar" {
		status = "paused_by_user"
	}
	if action == "reanudar" {
		status = "active"
	}
	query := `UPDATE jobs SET status=? WHERE id=?`
	args := []any{status, id}
	if action == "detener" {
		query = `DELETE FROM jobs WHERE id=?`
		args = []any{id}
	}
	if !admin {
		query += ` AND discord_user_id=?`
		args = append(args, userID)
	}
	result, err := d.DB.ExecContext(ctx, query, args...)
	if err != nil {
		return InteractionResponse{}, err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return message("No encontré esa búsqueda o no te pertenece."), nil
	}
	if d.OnJobsChanged != nil {
		d.OnJobsChanged()
	}
	if action == "reanudar" && d.OnJobCreated != nil {
		d.OnJobCreated(strconv.FormatInt(id, 10))
	}
	if action == "detener" {
		status = "detenida"
	}
	return message(fmt.Sprintf("Búsqueda #%d: %s.", id, status)), nil
}

func (d CommandDispatcher) adminStats(ctx context.Context) (InteractionResponse, error) {
	var users, jobs, paused, history int
	for _, item := range []struct {
		q string
		v *int
	}{{`SELECT COUNT(*) FROM users`, &users}, {`SELECT COUNT(*) FROM jobs`, &jobs}, {`SELECT COUNT(*) FROM users WHERE pause_reason IS NOT NULL`, &paused}, {`SELECT COUNT(*) FROM poll_outcome_history`, &history}} {
		if err := d.DB.QueryRowContext(ctx, item.q).Scan(item.v); err != nil {
			return InteractionResponse{}, err
		}
	}
	return message(fmt.Sprintf("Usuarios: %d\nBúsquedas: %d\nCuentas pausadas: %d\nResultados registrados: %d", users, jobs, paused, history)), nil
}

func (d CommandDispatcher) adminUserStats(ctx context.Context, userID string) (InteractionResponse, error) {
	if userID == "" {
		return message("Seleccioná un usuario."), nil
	}
	var jobs, commands int
	var reason sql.NullString
	err := d.DB.QueryRowContext(ctx, `SELECT pause_reason FROM users WHERE discord_user_id=?`, userID).Scan(&reason)
	if err == sql.ErrNoRows {
		return message("La cuenta no está registrada."), nil
	}
	if err != nil {
		return InteractionResponse{}, err
	}
	_ = d.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE discord_user_id=?`, userID).Scan(&jobs)
	_ = d.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM command_log WHERE discord_user_id=?`, userID).Scan(&commands)
	pause := "no"
	if reason.Valid {
		pause = reason.String
	}
	return message(fmt.Sprintf("Usuario: %s\nBúsquedas: %d\nComandos: %d\nPausa: %s", userID, jobs, commands, pause)), nil
}

func (d CommandDispatcher) autocomplete(ctx context.Context, userID string, in Interaction) (InteractionResponse, error) {
	admin := strings.HasPrefix(in.Data.Name, "admin-")
	if admin && !hasAdministrator(in.Member.Permissions) {
		return InteractionResponse{Type: 8, Data: map[string]any{"choices": []any{}}}, nil
	}
	choices := []any{}
	for _, option := range in.Data.Options {
		if option.Name == "usuario" {
			rows, err := d.DB.QueryContext(ctx, `SELECT discord_user_id FROM users ORDER BY discord_user_id LIMIT 25`)
			if err != nil {
				return InteractionResponse{}, err
			}
			defer rows.Close()
			for rows.Next() {
				var account string
				if err = rows.Scan(&account); err != nil {
					return InteractionResponse{}, err
				}
				choices = append(choices, map[string]any{"name": account, "value": account})
			}
			return InteractionResponse{Type: 8, Data: map[string]any{"choices": choices}}, rows.Err()
		}
	}
	query := `SELECT id,discord_user_id,COALESCE(label,'') FROM jobs`
	args := []any{}
	if !admin {
		query += ` WHERE discord_user_id=?`
		args = append(args, userID)
	}
	query += ` ORDER BY id DESC LIMIT 25`
	rows, err := d.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return InteractionResponse{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int
		var owner, label string
		if err = rows.Scan(&id, &owner, &label); err != nil {
			return InteractionResponse{}, err
		}
		name := fmt.Sprintf("#%d %s", id, label)
		if admin {
			name += ` · ` + owner
		}
		choices = append(choices, map[string]any{"name": name, "value": strconv.Itoa(id)})
	}
	return InteractionResponse{Type: 8, Data: map[string]any{"choices": choices}}, rows.Err()
}

func (d CommandDispatcher) logCommand(ctx context.Context, userID, name, guild string) error {
	_, err := d.DB.ExecContext(ctx, `INSERT INTO command_log(discord_user_id,command_name,guild_id,created_at) VALUES(?,?,?,?)`, userID, name, nullable(guild), time.Now().UnixMilli())
	return err
}
func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}
func stringOption(options []Option, name string) string {
	for _, o := range options {
		if o.Name == name {
			return fmt.Sprint(o.Value)
		}
	}
	return ""
}
func csvValues(value string, upper bool) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if upper {
			part = strings.ToUpper(part)
		}
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
func hasAdministrator(value string) bool {
	permissions, err := strconv.ParseUint(value, 10, 64)
	return err == nil && permissions&8 != 0
}
