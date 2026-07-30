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
	"unicode/utf8"

	credentialcrypto "github.com/ogs/uade-bot/internal/crypto"
	"github.com/ogs/uade-bot/internal/sso"
	"github.com/ogs/uade-bot/internal/uade"
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
// interaction; Permissions is the decimal-string bitmask Discord sends.
// It is no longer used for admin authorization (see isAdmin) -- it is
// preserved only because it faithfully reflects Discord's own payload.
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

// IdentityResolver is the read-only, cache-only identity view supplied by the
// live Gateway projection. Implementations must resolve at call time.
type IdentityResolver interface {
	ResolveIdentity(guildID, userID string) string
}

// CommandDispatcher implements all global slash commands and modal submits.
// It does not accept a guild allow-list: ownership is account based.
//
// Admin authorization deliberately REPLACES the decision recorded in
// .planning/STATE.md under "[Phase 03 P04, live UAT]" (gating admin-*
// centrally via Discord's own per-guild Administrator permission bit). That
// was a conscious, explicitly user-requested architecture change, not a bug
// fix: admin access is now decoupled from a per-guild Discord permission and
// instead comes from a fixed super-admin identity (SuperAdminID, sourced
// from UADE_SUPER_ADMIN_ID and never editable at runtime) plus a durable
// `admins` table that only the super-admin can mutate via
// /superadmin-agregar and /superadmin-eliminar. See isAdmin.
type CommandDispatcher struct {
	DB             *sql.DB
	MasterKey      string
	// SuperAdminID is the fixed operator identity (UADE_SUPER_ADMIN_ID) that
	// always satisfies isAdmin and is the only identity allowed to mutate the
	// admins table via /superadmin-agregar and /superadmin-eliminar.
	SuperAdminID   string
	OnJobCreated   func(string)
	OnAccountReady func(string)
	// PrepareAccount is the production post-credential seam. When present,
	// submitCredentials detaches a bounded activation from Discord's request
	// deadline, waits for UADE relink, and only then materializes a pending job.
	PrepareAccount     func(context.Context, string) error
	OnAccountActivated func(account, excludeJobID string)
	OnJobsChanged      func()
	SendChannel        func(channelID, content string) error
	ResolveMateria     func(context.Context, string, string) (string, error)
	IdentityResolver   IdentityResolver
}

var credentialActivationTimeout = 2 * time.Minute

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
	if in.Type == 3 {
		return d.component(ctx, userID, in)
	}
	if in.Type != 2 {
		return InteractionResponse{}, errors.New("unsupported interaction type")
	}
	if d.DB == nil {
		return InteractionResponse{}, errors.New("database unavailable")
	}
	if in.Data.Name == "superadmin-agregar" || in.Data.Name == "superadmin-eliminar" {
		if d.SuperAdminID == "" || userID != d.SuperAdminID {
			return message("Este comando requiere ser el super-admin configurado."), nil
		}
	}
	admin := strings.HasPrefix(in.Data.Name, "admin-")
	if admin {
		ok, err := d.isAdmin(ctx, userID)
		if err != nil {
			return InteractionResponse{}, err
		}
		if !ok {
			return message("Este comando requiere permisos de administrador (tabla admins o super-admin)."), nil
		}
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
		return d.adminUserStats(ctx, in.GuildID, stringOption(in.Data.Options, "usuario"))
	case "superadmin-agregar":
		return d.superadminAgregar(ctx, userID, stringOption(in.Data.Options, "usuario"))
	case "superadmin-eliminar":
		return d.superadminEliminar(ctx, userID, stringOption(in.Data.Options, "usuario"))
	default:
		return message("Comando desconocido."), nil
	}
}

func message(content string) InteractionResponse {
	return InteractionResponse{Type: 4, Data: map[string]any{
		"content":          content,
		"flags":            ephemeral,
		"allowed_mentions": map[string]any{"parse": []string{}},
	}}
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
	if d.PrepareAccount != nil {
		go d.activateCredentials(userID)
		return message("Credenciales guardadas de forma cifrada. La activación de tus búsquedas está en curso."), nil
	}
	if d.OnAccountReady != nil {
		d.OnAccountReady(userID)
	}
	activation, jobErr := d.materializePendingSearchResult(ctx, userID)
	if jobErr != nil {
		return message("Credenciales guardadas, pero no pude validar la materia pendiente en UADE. No creé la búsqueda; volvé a intentar."), nil
	}
	if activation.id != "" {
		if activation.created && d.OnJobCreated != nil {
			d.OnJobCreated(activation.id)
		}
		var label, rawFilters string
		if err := d.DB.QueryRowContext(ctx, `SELECT COALESCE(label,''),filtros_json FROM jobs WHERE id=? AND discord_user_id=?`, activation.id, userID).Scan(&label, &rawFilters); err != nil {
			return InteractionResponse{}, err
		}
		filters := parseJobFilters(rawFilters)
		summary := filterSummaryBlock(label, filters.MateriaCodigo, lookupMateriaNombre(ctx, d.DB, filters.MateriaCodigo), filters.Turno, filters.Ofrecimiento, filters.Dias, filters.SedesExcluidas)
		return message("Credenciales guardadas de forma cifrada. Ya creé la búsqueda que habías pedido con /buscar.\n\n" + summary), nil
	}
	return message("Credenciales guardadas de forma cifrada. Todavía no creé ninguna búsqueda: volvé a usar /buscar para crearla."), nil
}

func (d CommandDispatcher) activateCredentials(userID string) {
	defer func() { _ = recover() }()
	ctx, cancel := context.WithTimeout(context.Background(), credentialActivationTimeout)
	defer cancel()
	if err := d.PrepareAccount(ctx, userID); err != nil {
		return
	}
	activation, err := d.materializePendingSearchResult(ctx, userID)
	if err != nil {
		if d.OnAccountActivated != nil {
			d.OnAccountActivated(userID, "")
		}
		return
	}
	if activation.created && d.OnJobCreated != nil {
		d.OnJobCreated(activation.id)
	}
	if d.OnAccountActivated != nil {
		exclude := ""
		if activation.created {
			exclude = activation.id
		}
		d.OnAccountActivated(userID, exclude)
	}
}

// savePendingSearch persists the exact filters/channel/guild/label of a
// /buscar request made while the user has no saved credentials yet, so
// submitCredentials can materialize it later instead of losing it. Upserts
// by discord_user_id: a second /buscar before the modal is completed
// overwrites the previous pending row rather than accumulating rows.
func (d CommandDispatcher) savePendingSearch(ctx context.Context, userID, channelID, guildID, label, filtersJSON string) error {
	_, err := d.DB.ExecContext(ctx, `INSERT INTO pending_searches(discord_user_id,filtros_json,channel_id,guild_id,label,created_at) VALUES(?,?,?,?,?,?) ON CONFLICT(discord_user_id) DO UPDATE SET filtros_json=excluded.filtros_json,channel_id=excluded.channel_id,guild_id=excluded.guild_id,label=excluded.label,created_at=CASE WHEN excluded.created_at <= pending_searches.created_at THEN pending_searches.created_at + 1 ELSE excluded.created_at END`, userID, filtersJSON, nullable(channelID), nullable(guildID), label, time.Now().UnixMilli())
	return err
}

type jobCreation struct {
	id      string
	created bool
}

// materializePendingSearch converts a saved pending_searches row (if any)
// into an active job with the exact filters originally requested, then
// deletes the pending row. Returns "" (no error) when there is nothing
// pending -- that's the normal case, not a failure.
func (d CommandDispatcher) materializePendingSearch(ctx context.Context, userID string) (string, error) {
	result, err := d.materializePendingSearchResult(ctx, userID)
	return result.id, err
}

func (d CommandDispatcher) materializePendingSearchResult(ctx context.Context, userID string) (jobCreation, error) {
	for {
		var filtersJSON, label string
		var channelID, guildID sql.NullString
		var version int64
		err := d.DB.QueryRowContext(ctx, `SELECT filtros_json,channel_id,guild_id,label,created_at FROM pending_searches WHERE discord_user_id=?`, userID).Scan(&filtersJSON, &channelID, &guildID, &label, &version)
		if err == sql.ErrNoRows {
			return jobCreation{}, nil
		}
		if err != nil {
			return jobCreation{}, err
		}
		filters := parseJobFilters(filtersJSON)
		materiaName, err := d.resolveMateriaName(ctx, userID, filters.MateriaCodigo)
		if err != nil {
			return jobCreation{}, err
		}
		tx, err := d.DB.BeginTx(ctx, nil)
		if err != nil {
			return jobCreation{}, err
		}
		deleted, err := tx.ExecContext(ctx, `DELETE FROM pending_searches WHERE discord_user_id=? AND created_at=?`, userID, version)
		if err != nil {
			tx.Rollback()
			return jobCreation{}, err
		}
		matched, err := deleted.RowsAffected()
		if err != nil {
			tx.Rollback()
			return jobCreation{}, err
		}
		if matched == 0 {
			tx.Rollback()
			continue
		}
		now := time.Now().UnixMilli()
		if materiaName != "" {
			if _, err = tx.ExecContext(ctx, `INSERT INTO materias(codigo,nombre,updated_at) VALUES(?,?,?) ON CONFLICT(codigo) DO UPDATE SET nombre=excluded.nombre,updated_at=excluded.updated_at`, filters.MateriaCodigo, materiaName, now); err != nil {
				tx.Rollback()
				return jobCreation{}, err
			}
		}
		inserted, err := tx.ExecContext(ctx, `INSERT INTO jobs(discord_user_id,filtros_json,materia_code,channel_id,guild_id,label,status,created_at) VALUES(?,?,?,?,?,?,'active',?)`, userID, filtersJSON, filters.MateriaCodigo, channelID, guildID, label, now)
		if err != nil {
			tx.Rollback()
			existing, findErr := d.findExistingJob(ctx, userID, guildID.String, filters.MateriaCodigo)
			if findErr != nil || existing == "" {
				return jobCreation{}, err
			}
			_, deleteErr := d.DB.ExecContext(ctx, `DELETE FROM pending_searches WHERE discord_user_id=? AND created_at=?`, userID, version)
			if deleteErr != nil {
				return jobCreation{}, deleteErr
			}
			return jobCreation{id: existing}, nil
		}
		id, err := inserted.LastInsertId()
		if err != nil {
			tx.Rollback()
			return jobCreation{}, err
		}
		if err = tx.Commit(); err != nil {
			return jobCreation{}, err
		}
		return jobCreation{id: strconv.FormatInt(id, 10), created: true}, nil
	}
}

func (d CommandDispatcher) findExistingJob(ctx context.Context, userID, guildID, code string) (string, error) {
	var id string
	err := d.DB.QueryRowContext(ctx, `SELECT id FROM jobs WHERE discord_user_id=? AND COALESCE(guild_id,'')=? AND materia_code=? ORDER BY id LIMIT 1`, userID, guildID, code).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return id, err
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
	dias := filterValidDias(stringOption(options, "dias"))
	if len(dias) == 0 {
		return message("Los dias tienen que ser alguno de LU, MA, MI, JU, VI, SA."), nil
	}
	turno := stringOption(options, "turno")
	ofrecimiento := stringOption(options, "ofrecimiento")
	sedes := csvValues(stringOption(options, "sedes_excluidas"), false)
	filters, _ := json.Marshal(map[string]any{"materiaCodigo": code, "turno": turno, "ofrecimiento": ofrecimiento, "dias": dias, "sedesExcluidas": sedes})
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
	if existing, err := d.findExistingJob(ctx, userID, guildID, code); err != nil {
		return InteractionResponse{}, err
	} else if existing != "" {
		return message(fmt.Sprintf("La materia `%s` ya está siendo monitoreada en esta ubicación (búsqueda #%s).", code, existing)), nil
	}
	materiaName, resolveErr := d.resolveMateriaName(ctx, userID, code)
	if resolveErr != nil {
		return message("No pude validar esa materia en UADE. No creé la búsqueda; volvé a intentar."), nil
	}
	now := time.Now().UnixMilli()
	tx, err := d.DB.BeginTx(ctx, nil)
	if err != nil {
		return InteractionResponse{}, err
	}
	defer tx.Rollback()
	if materiaName != "" {
		if _, err = tx.ExecContext(ctx, `INSERT INTO materias(codigo,nombre,updated_at) VALUES(?,?,?) ON CONFLICT(codigo) DO UPDATE SET nombre=excluded.nombre,updated_at=excluded.updated_at`, code, materiaName, now); err != nil {
			return InteractionResponse{}, err
		}
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO jobs(discord_user_id,filtros_json,materia_code,channel_id,guild_id,label,status,created_at) VALUES(?,?,?,?,?,?,'active',?)`, userID, string(filters), code, nullable(channelID), nullable(guildID), label, now)
	if err != nil {
		tx.Rollback()
		existing, findErr := d.findExistingJob(ctx, userID, guildID, code)
		if findErr == nil && existing != "" {
			return message(fmt.Sprintf("La materia `%s` ya está siendo monitoreada en esta ubicación (búsqueda #%s).", code, existing)), nil
		}
		return InteractionResponse{}, err
	}
	if err = tx.Commit(); err != nil {
		return InteractionResponse{}, err
	}
	id, idErr := result.LastInsertId()
	if d.OnJobCreated != nil {
		if idErr == nil {
			d.OnJobCreated(strconv.FormatInt(id, 10))
		}
	}
	summary := filterSummaryBlock(label, code, materiaName, turno, ofrecimiento, dias, sedes)
	return message(summary + "\n**Monitoreo:** activo"), nil
}

func (d CommandDispatcher) resolveMateriaName(ctx context.Context, userID, code string) (string, error) {
	var cached string
	err := d.DB.QueryRowContext(ctx, `SELECT nombre FROM materias WHERE codigo=?`, code).Scan(&cached)
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}
	if err == nil {
		clean := uade.NormalizeMateriaName(cached)
		if clean != "" {
			if clean != strings.TrimSpace(cached) {
				_, updateErr := d.DB.ExecContext(ctx, `UPDATE materias SET nombre=?,updated_at=? WHERE codigo=?`, clean, time.Now().UnixMilli(), code)
				if updateErr != nil {
					return "", updateErr
				}
			}
			return clean, nil
		}
	}
	if d.ResolveMateria == nil {
		return "", nil
	}
	name, err := d.ResolveMateria(ctx, userID, code)
	if err != nil {
		return "", err
	}
	name = uade.NormalizeMateriaName(name)
	if name == "" {
		return "", errors.New("materia resolver returned empty name")
	}
	return name, nil
}

func (d CommandDispatcher) estado(ctx context.Context, userID string, admin bool, filter string) (InteractionResponse, error) {
	type jobRow struct {
		id                 int64
		owner, label       string
		status, rawFilters string
		channelID, guildID sql.NullString
		lastPolled         sql.NullInt64
		lastOutcome        string
		accountPauseReason sql.NullString
	}

	query := `SELECT j.id,j.discord_user_id,COALESCE(j.label,''),j.status,j.filtros_json,j.channel_id,j.guild_id,j.last_polled_at,COALESCE(j.last_outcome,''),u.pause_reason FROM jobs j LEFT JOIN users u ON u.discord_user_id=j.discord_user_id`
	args := []any{}
	if !admin {
		query += ` WHERE j.discord_user_id=?`
		args = append(args, userID)
	} else if filter != "" {
		query += ` WHERE j.discord_user_id=?`
		args = append(args, filter)
	}
	query += ` ORDER BY j.id LIMIT 25`
	rows, err := d.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return InteractionResponse{}, err
	}
	jobs := make([]jobRow, 0, 25)
	for rows.Next() {
		var job jobRow
		if err = rows.Scan(&job.id, &job.owner, &job.label, &job.status, &job.rawFilters, &job.channelID, &job.guildID, &job.lastPolled, &job.lastOutcome, &job.accountPauseReason); err != nil {
			rows.Close()
			return InteractionResponse{}, err
		}
		jobs = append(jobs, job)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return InteractionResponse{}, err
	}
	if err = rows.Close(); err != nil {
		return InteractionResponse{}, err
	}

	lines := []string{}
	for _, job := range jobs {
		filters := parseJobFilters(job.rawFilters)
		materia := filters.MateriaCodigo
		if name := lookupMateriaNombre(ctx, d.DB, filters.MateriaCodigo); name != "" {
			materia += " - " + name
		}
		lastOutcome := lastOutcomeLine(ctx, d.DB, job.id, job.lastOutcome)
		label := job.label
		if label == "" {
			label = "sin etiqueta"
		}
		if admin {
			identity := materia
			if label != filters.MateriaCodigo {
				identity = label + " - " + materia
			}
			status := formatJobStatusText(job.status, sql.NullString{})
			lines = append(lines, fmt.Sprintf("**#%d** %s · %s · %s %s · %s · %s · %s", job.id, d.adminIdentity(job.guildID.String, job.owner), identity, filters.Turno, strings.Join(filters.Dias, "/"), status, formatJobLocation(job.guildID, job.channelID), lastOutcome))
		} else {
			block := fmt.Sprintf("**%s**\n**Materia:** %s\n**Turno:** %s\n**Dias:** %s\n", label, materia, filters.Turno, strings.Join(filters.Dias, ", "))
			if len(filters.SedesExcluidas) > 0 {
				block += fmt.Sprintf("**Sedes excluidas:** %s\n", strings.Join(filters.SedesExcluidas, ", "))
			}
			block += fmt.Sprintf("**Server/Canal:** %s\n**Estado:** %s\n**Ultimo sondeo:** %s\n**Ultimo resultado:** %s", formatJobLocation(job.guildID, job.channelID), formatJobStatusText(job.status, job.accountPauseReason), formatLastPoll(job.lastPolled), lastOutcome)
			lines = append(lines, block)
		}
	}
	pending, err := d.pendingSearchLine(ctx, admin, userID, filter)
	if err != nil {
		return InteractionResponse{}, err
	}
	if pending != "" {
		lines = append(lines, pending)
	}
	if len(lines) == 0 {
		return message("No hay búsquedas para mostrar."), nil
	}
	if admin {
		return message(truncateAdminList(lines)), nil
	}
	return message(strings.Join(lines, "\n\n")), nil
}

func (d CommandDispatcher) pendingSearchLine(ctx context.Context, admin bool, userID, filter string) (string, error) {
	query := `SELECT discord_user_id,COALESCE(label,''),guild_id FROM pending_searches`
	args := []any{}
	if !admin {
		query += ` WHERE discord_user_id=?`
		args = append(args, userID)
	} else if filter != "" {
		query += ` WHERE discord_user_id=?`
		args = append(args, filter)
	}
	query += ` ORDER BY created_at`
	rows, err := d.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return "", err
	}
	lines := []string{}
	for rows.Next() {
		var owner, label string
		var guildID sql.NullString
		if err = rows.Scan(&owner, &label, &guildID); err != nil {
			rows.Close()
			return "", err
		}
		if label == "" {
			label = "sin etiqueta"
		}
		if admin {
			lines = append(lines, fmt.Sprintf("⏳ %s · %s · pendiente de credenciales", d.adminIdentity(guildID.String, owner), label))
		} else {
			lines = append(lines, fmt.Sprintf("⏳ **%s** — pendiente: todavía no completaste el modal de credenciales para esta búsqueda. Volvé a intentar con `/credenciales`.", label))
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return "", err
	}
	if err = rows.Close(); err != nil {
		return "", err
	}
	separator := "\n\n"
	if admin {
		separator = "\n"
	}
	return strings.Join(lines, separator), nil
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

func (d CommandDispatcher) adminUserStats(ctx context.Context, guildID, userID string) (InteractionResponse, error) {
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
	return message(fmt.Sprintf("Usuario: %s\nBúsquedas: %d\nComandos: %d\nPausa: %s", d.adminIdentity(guildID, userID), jobs, commands, pause)), nil
}

// superadminAgregar grants admin-* access to targetID by upserting a row in
// the admins table (added_by is always the super-admin authenticated by
// DispatchInteraction's earlier gate, never a client-supplied value -- see
// T-260730-gav-03). Rejects targetID == d.SuperAdminID: the super-admin's
// access never depends on this table, so a row for it would be redundant
// (T-260730-gav-05).
func (d CommandDispatcher) superadminAgregar(ctx context.Context, superAdminID, targetID string) (InteractionResponse, error) {
	if targetID == "" {
		return message("Seleccioná un usuario."), nil
	}
	if targetID == d.SuperAdminID {
		return message("El super-admin ya tiene acceso de administrador siempre; no hace falta ni se permite agregarlo a la tabla admins."), nil
	}
	_, err := d.DB.ExecContext(ctx, `INSERT INTO admins(discord_user_id,added_by,created_at) VALUES(?,?,?) ON CONFLICT(discord_user_id) DO UPDATE SET added_by=excluded.added_by,created_at=excluded.created_at`, targetID, superAdminID, time.Now().UnixMilli())
	if err != nil {
		return InteractionResponse{}, err
	}
	return message(fmt.Sprintf("Agregué a <@%s> como admin.", targetID)), nil
}

// superadminEliminar revokes targetID's row in the admins table, if any.
// Rejects targetID == d.SuperAdminID for the same reason as
// superadminAgregar: the super-admin's access is never sourced from this
// table.
func (d CommandDispatcher) superadminEliminar(ctx context.Context, _, targetID string) (InteractionResponse, error) {
	if targetID == "" {
		return message("Seleccioná un usuario."), nil
	}
	if targetID == d.SuperAdminID {
		return message("El super-admin ya tiene acceso de administrador siempre; no hace falta ni se permite eliminarlo de la tabla admins."), nil
	}
	result, err := d.DB.ExecContext(ctx, `DELETE FROM admins WHERE discord_user_id=?`, targetID)
	if err != nil {
		return InteractionResponse{}, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return InteractionResponse{}, err
	}
	if n == 0 {
		return message(fmt.Sprintf("<@%s> no estaba en la tabla de admins.", targetID)), nil
	}
	return message(fmt.Sprintf("<@%s> ya no es admin.", targetID)), nil
}

func (d CommandDispatcher) autocomplete(ctx context.Context, userID string, in Interaction) (InteractionResponse, error) {
	admin := strings.HasPrefix(in.Data.Name, "admin-")
	if admin {
		ok, err := d.isAdmin(ctx, userID)
		if err != nil {
			return InteractionResponse{}, err
		}
		if !ok {
			return InteractionResponse{Type: 8, Data: map[string]any{"choices": []any{}}}, nil
		}
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
				choices = append(choices, map[string]any{"name": truncateChoiceName(d.adminIdentity(in.GuildID, account)), "value": account})
			}
			return InteractionResponse{Type: 8, Data: map[string]any{"choices": choices}}, rows.Err()
		}
	}
	type jobChoiceRow struct {
		id           int
		owner, label string
		guildID      sql.NullString
		rawFilters   string
	}

	query := `SELECT id,discord_user_id,COALESCE(label,''),guild_id,filtros_json FROM jobs`
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
	jobs := make([]jobChoiceRow, 0, 25)
	for rows.Next() {
		var job jobChoiceRow
		if err = rows.Scan(&job.id, &job.owner, &job.label, &job.guildID, &job.rawFilters); err != nil {
			rows.Close()
			return InteractionResponse{}, err
		}
		jobs = append(jobs, job)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return InteractionResponse{}, err
	}
	if err = rows.Close(); err != nil {
		return InteractionResponse{}, err
	}

	// rows is drained to memory and closed above, BEFORE this loop's nested
	// lookupMateriaNombre query -- same single-connection pattern estado()
	// already resolved (store.Open's SetMaxOpenConns(1) hangs forever on a
	// nested query while the outer *sql.Rows is still open).
	for _, job := range jobs {
		filters := parseJobFilters(job.rawFilters)
		materiaNombre := lookupMateriaNombre(ctx, d.DB, filters.MateriaCodigo)
		detail := formatJobChoiceLabel(job.label, filters, materiaNombre)
		var name string
		if admin {
			name = fmt.Sprintf("#%d %s · %s", job.id, detail, d.adminIdentity(job.guildID.String, job.owner))
		} else {
			name = detail
		}
		choices = append(choices, map[string]any{"name": truncateChoiceName(name), "value": strconv.Itoa(job.id)})
	}
	return InteractionResponse{Type: 8, Data: map[string]any{"choices": choices}}, nil
}

func (d CommandDispatcher) adminIdentity(guildID, userID string) string {
	name := userID
	if d.IdentityResolver != nil {
		name = strings.TrimSpace(d.IdentityResolver.ResolveIdentity(guildID, userID))
	}
	if name == "" || name == userID {
		return escapeDiscordIdentity(userID)
	}
	return fmt.Sprintf("%s (%s)", escapeDiscordIdentity(name), escapeDiscordIdentity(userID))
}

func escapeDiscordIdentity(value string) string {
	return strings.NewReplacer(
		`\`, `\\`, "*", `\*`, "_", `\_`, "~", `\~`, "`", "\\`",
		"|", `\|`, ">", `\>`, "<", `\<`,
	).Replace(value)
}

func truncateChoiceName(value string) string {
	if utf8.RuneCountInString(value) <= 100 {
		return value
	}
	return string([]rune(value)[:99]) + "…"
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
// isAdmin is the sole authorization mechanism for the admin-* commands
// (see CommandDispatcher's doc comment). userID satisfies it either by
// being the fixed super-admin (SuperAdminID, compared exactly -- an empty
// SuperAdminID never matches an empty userID) or by having a row in the
// admins table. It never reads Member.Permissions or any other
// Discord-supplied permission bit.
func (d CommandDispatcher) isAdmin(ctx context.Context, userID string) (bool, error) {
	if d.SuperAdminID != "" && userID == d.SuperAdminID {
		return true, nil
	}
	var count int
	if err := d.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM admins WHERE discord_user_id=?`, userID).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}
