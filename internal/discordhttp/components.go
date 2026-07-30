package discordhttp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
)

const detenerButtonPrefix = "detener_job:"

func parseDetenerButtonID(customID string) (int64, bool) {
	if !strings.HasPrefix(customID, detenerButtonPrefix) {
		return 0, false
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(customID, detenerButtonPrefix), 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}

func (d CommandDispatcher) component(ctx context.Context, userID string, in Interaction) (InteractionResponse, error) {
	id, ok := parseDetenerButtonID(in.Data.CustomID)
	if !ok {
		return message("Boton desconocido."), nil
	}
	if d.DB == nil {
		return InteractionResponse{}, errors.New("database unavailable")
	}

	var label string
	var channelID sql.NullString
	err := d.DB.QueryRowContext(ctx, `SELECT COALESCE(label,''), channel_id FROM jobs WHERE id=? AND discord_user_id=?`, id, userID).Scan(&label, &channelID)
	if err == sql.ErrNoRows {
		return message("No encontré esa búsqueda o no te pertenece."), nil
	}
	if err != nil {
		return InteractionResponse{}, err
	}
	if _, err = d.DB.ExecContext(ctx, `DELETE FROM jobs WHERE id=?`, id); err != nil {
		return InteractionResponse{}, err
	}
	if d.OnJobsChanged != nil {
		d.OnJobsChanged()
	}

	confirmation := fmt.Sprintf("Búsqueda #%d: detenida.", id)
	if d.SendChannel != nil && channelID.String != "" && channelID.String != in.ChannelID {
		echo := fmt.Sprintf("Búsqueda #%d (%s): detenida.", id, label)
		if err = d.SendChannel(channelID.String, echo); err != nil {
			log.Printf("detener channel echo failed job_id=%d", id)
		}
	}
	return message(confirmation), nil
}
