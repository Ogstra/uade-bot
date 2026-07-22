package discordhttp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	credentialcrypto "github.com/ogs/uade-bot/internal/crypto"
)

const ephemeral = 1 << 6

var materiaPattern = regexp.MustCompile(`^\d+(?:\.\d+){2}$`)

type interaction struct {
	Type      int    `json:"type"`
	GuildID   string `json:"guild_id"`
	ChannelID string `json:"channel_id"`
	Data      struct {
		Name     string          `json:"name"`
		CustomID string          `json:"custom_id"`
		Options  []option        `json:"options"`
		Values   json.RawMessage `json:"components"`
	} `json:"data"`
	Member struct {
		Permissions string `json:"permissions"`
		User        user   `json:"user"`
	} `json:"member"`
	User user `json:"user"`
}
type user struct {
	ID string `json:"id"`
}
type option struct {
	Name    string `json:"name"`
	Type    int    `json:"type"`
	Value   any    `json:"value"`
	Focused bool   `json:"focused"`
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

func (d CommandDispatcher) Dispatch(ctx context.Context, body []byte) (InteractionResponse, error) {
	var in interaction
	if err := json.Unmarshal(body, &in); err != nil {
		return InteractionResponse{}, err
	}
	userID := in.Member.User.ID
	if userID == "" {
		userID = in.User.ID
	}
	if userID == "" {
		return message("No pude identificar tu cuenta."), nil
	}
	if in.Type == 5 {
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
		return credentialsModal(stringOption(in.Data.Options, "modo")), nil
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

func credentialsModal(mode string) InteractionResponse {
	if mode == "" {
		mode = "todo"
	}
	fields := []any{}
	add := func(id, label string, style int) {
		fields = append(fields, map[string]any{"type": 1, "components": []any{map[string]any{
			"type": 4, "custom_id": id, "label": label, "style": style, "required": true,
		}}})
	}
	if mode != "link" {
		add("uade_username", "Usuario UADE", 1)
		add("uade_password", "Password UADE", 1)
	}
	add("uade_start_url", "Link de inscripción", 2)
	return InteractionResponse{Type: 9, Data: map[string]any{
		"custom_id": "credentials:" + mode, "title": "Credenciales UADE", "components": fields,
	}}
}

func (d CommandDispatcher) submitCredentials(ctx context.Context, userID, customID string, raw json.RawMessage) (InteractionResponse, error) {
	if !strings.HasPrefix(customID, "credentials:") {
		return message("Formulario desconocido."), nil
	}
	values := modalValues(raw)
	startURL := values["uade_start_url"]
	if !validStartURL(startURL) {
		return message("El link debe ser de inscripcionespia.uade.edu.ar e incluir param=. No se guardó nada."), nil
	}
	mode := strings.TrimPrefix(customID, "credentials:")
	creds := credentialcrypto.Credentials{UADEUsername: values["uade_username"], UADEPassword: values["uade_password"], UADEStartURL: startURL}
	if mode == "link" || mode == "usuario_password" {
		current, err := d.readCredentials(ctx, userID)
		if err != nil {
			return message("Primero cargá todas tus credenciales con /credenciales modo:Todo."), nil
		}
		if mode == "link" {
			current.UADEStartURL = startURL
		} else {
			current = creds
		}
		creds = current
	}
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
	return message("Credenciales guardadas de forma cifrada. Tus búsquedas quedan listas para continuar."), nil
}

func (d CommandDispatcher) readCredentials(ctx context.Context, userID string) (credentialcrypto.Credentials, error) {
	var c credentialcrypto.Ciphertext
	err := d.DB.QueryRowContext(ctx, `SELECT ciphertext,iv,auth_tag FROM credentials WHERE discord_user_id=?`, userID).Scan(&c.Ciphertext, &c.IV, &c.AuthTag)
	if err != nil {
		return credentialcrypto.Credentials{}, err
	}
	return credentialcrypto.Decrypt(d.MasterKey, userID, c)
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

func validStartURL(value string) bool {
	u, err := url.Parse(value)
	return err == nil && u.Scheme == "https" && strings.EqualFold(u.Hostname(), "inscripcionespia.uade.edu.ar") && u.Query().Has("param")
}

func (d CommandDispatcher) buscar(ctx context.Context, userID, channelID, guildID string, options []option) (InteractionResponse, error) {
	code := strings.TrimSpace(stringOption(options, "cod_materia"))
	if !materiaPattern.MatchString(code) {
		return message("El código de materia no es válido (ejemplo: 3.1.050)."), nil
	}
	var credentials int
	if err := d.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM credentials WHERE discord_user_id=?`, userID).Scan(&credentials); err != nil {
		return InteractionResponse{}, err
	}
	if credentials == 0 {
		return message("Primero cargá tus credenciales con /credenciales; se abrirá un formulario privado."), nil
	}
	var active int
	if err := d.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE discord_user_id=? AND status IN ('active','paused_by_user')`, userID).Scan(&active); err != nil {
		return InteractionResponse{}, err
	}
	if active >= 10 {
		return message("Ya tenés 10 búsquedas activas o pausadas."), nil
	}
	filters, _ := json.Marshal(map[string]any{"materiaCodigo": code, "turno": stringOption(options, "turno"), "ofrecimiento": stringOption(options, "ofrecimiento"), "dias": csvValues(stringOption(options, "dias"), true), "sedesExcluidas": csvValues(stringOption(options, "sedes_excluidas"), false)})
	now := time.Now().UnixMilli()
	label := stringOption(options, "etiqueta")
	if label == "" {
		label = code
	}
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

func (d CommandDispatcher) autocomplete(ctx context.Context, userID string, in interaction) (InteractionResponse, error) {
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
func stringOption(options []option, name string) string {
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
