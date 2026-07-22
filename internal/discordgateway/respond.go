// Package discordgateway boots a Discord Gateway (outbound WSS) session that
// carries both presence and interaction handling, replacing the HTTP
// interactions webhook transport (see 03.3-CONTEXT.md D-19/D-20/D-21): the
// VPS this bot runs on is never reachable over public HTTPS, so Discord must
// be able to deliver interactions over a connection the bot itself opens.
package discordgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/ogs/uade-bot/internal/discordhttp"
)

// Respond posts an interaction callback response directly to Discord's REST
// API, mirroring internal/discordhttp/register.go's raw net/http idiom
// instead of using disgo's REST client. This avoids disgo's sealed
// discord.InteractionResponseData interface (its unexported
// interactionCallbackData() method means only disgo-internal types can
// satisfy it) and deliberately sends no Authorization header: the callback
// endpoint authenticates via the interaction id+token already present in the
// URL (Discord's NewNoBotAuthEndpoint), not via the bot token. A failed or
// slow callback is not retried -- by the time a retry would land, Discord has
// likely already shown the interaction as failed to the user.
func Respond(ctx context.Context, client *http.Client, apiBase, id, token string, response discordhttp.InteractionResponse) error {
	if client == nil {
		client = http.DefaultClient
	}
	if apiBase == "" {
		apiBase = "https://discord.com/api/v10"
	}
	body, err := json.Marshal(response)
	if err != nil {
		return err
	}
	endpoint := apiBase + "/interactions/" + id + "/" + token + "/callback"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("discord interaction callback: status %d", resp.StatusCode)
	}
	return nil
}
