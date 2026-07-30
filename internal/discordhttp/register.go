package discordhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"
)

type commandOption map[string]any
type globalCommand struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Type        int             `json:"type"`
	Options     []commandOption `json:"options,omitempty"`
}

func opt(name, description string, required, autocomplete bool) commandOption {
	value := commandOption{"type": 3, "name": name, "description": description, "required": required}
	if autocomplete {
		value["autocomplete"] = true
	}
	return value
}

// GlobalCommands is the single registration source for all twelve commands.
// Registration deliberately uses the application-global endpoint; there is no
// guild ID argument or guild-specific fallback.
func GlobalCommands() []globalCommand {
	buscar := []commandOption{
		opt("cod_materia", "Codigo de materia, por ejemplo 3.1.050", true, false),
		opt("turno", "Turno a buscar", true, false), opt("ofrecimiento", "Tipo de ofrecimiento", true, false),
		opt("dias", "Dias separados por coma: LU,MA,MI,JU,VI,SA", true, false),
		opt("sedes_excluidas", "Sedes a excluir, separadas por coma", false, false), opt("etiqueta", "Nombre corto para reconocer esta busqueda", false, false),
	}
	buscar[1]["choices"] = []map[string]string{{"name": "Mañana", "value": "Mañana"}, {"name": "Tarde", "value": "Tarde"}, {"name": "Noche", "value": "Noche"}, {"name": "Intensivo", "value": "Intensivo"}, {"name": "Online", "value": "Online"}}
	buscar[2]["choices"] = []map[string]string{{"name": "Curricular", "value": "curricular"}, {"name": "Optativa", "value": "optativa"}}
	job := func(description string) []commandOption {
		return []commandOption{opt("busqueda", description, true, true)}
	}
	user := func(required bool) []commandOption {
		return []commandOption{opt("usuario", "Cuenta a consultar", required, true)}
	}
	return []globalCommand{
		{"buscar", "Crear una busqueda de vacantes en UADE", 1, buscar}, {"estado", "Ver tus busquedas activas o pausadas", 1, nil},
		{"detener", "Detener una de tus busquedas", 1, job("Busqueda a detener")}, {"pausar", "Pausar una de tus busquedas", 1, job("Busqueda a pausar")},
		{"reanudar", "Reanudar una de tus busquedas pausadas", 1, job("Busqueda a reanudar")}, {"credenciales", "Cargar o actualizar usuario y password de UADE", 1, nil},
		{"admin-estado", "[Admin] Ver todas las busquedas", 1, user(false)}, {"admin-detener", "[Admin] Detener cualquier busqueda", 1, job("Busqueda a detener")},
		{"admin-pausar", "[Admin] Pausar cualquier busqueda", 1, job("Busqueda a pausar")}, {"admin-reanudar", "[Admin] Reanudar cualquier busqueda", 1, job("Busqueda a reanudar")},
		{"admin-stats", "[Admin] Estadisticas generales del bot", 1, nil}, {"admin-user-stats", "[Admin] Estadisticas de una cuenta", 1, user(true)},
	}
}

func RegisterGlobal(ctx context.Context, client *http.Client, apiBase, token, applicationID string) error {
	return convergeCommands(ctx, client, apiBase, token, applicationID, nil, false)
}

// ConvergeCommands atomically replaces the application's global command set,
// then clears only this same application's guild-scoped sets. Guild IDs are
// normalized so reconnect/retry calls are deterministic and idempotent.
func ConvergeCommands(ctx context.Context, client *http.Client, apiBase, token, applicationID string, guildIDs []string) error {
	return convergeCommands(ctx, client, apiBase, token, applicationID, guildIDs, true)
}

func convergeCommands(ctx context.Context, client *http.Client, apiBase, token, applicationID string, guildIDs []string, cleanup bool) error {
	if applicationID == "" || token == "" {
		return fmt.Errorf("discord application id and bot token are required")
	}
	if client == nil {
		client = http.DefaultClient
	}
	if apiBase == "" {
		apiBase = "https://discord.com/api/v10"
	}
	body, err := json.Marshal(GlobalCommands())
	if err != nil {
		return err
	}
	endpoint := apiBase + "/applications/" + applicationID + "/commands"
	if err := putCommandSet(ctx, client, endpoint, token, body, "global"); err != nil {
		return err
	}
	if !cleanup {
		return nil
	}
	unique := make(map[string]struct{}, len(guildIDs))
	for _, id := range guildIDs {
		if id != "" {
			unique[id] = struct{}{}
		}
	}
	ordered := make([]string, 0, len(unique))
	for id := range unique {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	for _, guildID := range ordered {
		guildEndpoint := apiBase + "/applications/" + applicationID + "/guilds/" + guildID + "/commands"
		if err := putCommandSet(ctx, client, guildEndpoint, token, []byte("[]"), "guild:"+guildID); err != nil {
			return err
		}
	}
	return nil
}

func putCommandSet(ctx context.Context, client *http.Client, endpoint, token string, body []byte, stage string) error {
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bot "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}
		if resp.StatusCode != http.StatusTooManyRequests {
			return fmt.Errorf("discord command convergence stage %s: status %d", stage, resp.StatusCode)
		}
		delay := time.Second
		if value := resp.Header.Get("Retry-After"); value != "" {
			if seconds, e := strconv.ParseFloat(value, 64); e == nil {
				delay = time.Duration(seconds * float64(time.Second))
			}
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return fmt.Errorf("discord command convergence stage %s remained rate limited", stage)
}
