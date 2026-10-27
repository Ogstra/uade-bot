package discordhttp

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Ogstra/uade-bot/internal/entra"
)

// MicrosoftSessions is the slice of entra.Manager the /microsoft command
// needs. Every user signs in with their own account: there is no admin gate,
// because each session belongs to whoever opened it and touches nobody else.
type MicrosoftSessions interface {
	BeginLogin(ctx context.Context, userID string) (entra.LoginStatus, error)
	Status(userID string) (entra.Status, error)
	Forget(userID string) error
}

var argentina = func() *time.Location {
	if loc, err := time.LoadLocation("America/Argentina/Buenos_Aires"); err == nil {
		return loc
	}
	return time.FixedZone("ART", -3*60*60)
}()

// microsoft handles /microsoft. Every reply is ephemeral (card() sets the
// flag): the user code is short-lived but whoever reads it in a shared
// channel could race the owner to it and bind their own account instead.
func (d CommandDispatcher) microsoft(ctx context.Context, userID, accion string) (InteractionResponse, error) {
	if d.Microsoft == nil {
		return card(cardWarning, "El inicio de sesión con Microsoft no está configurado en este servidor."), nil
	}
	switch accion {
	case "", "iniciar":
		status, err := d.Microsoft.BeginLogin(ctx, userID)
		if err != nil || status.State != entra.LoginPending {
			message := status.Message
			if message == "" || errors.Is(err, entra.ErrInvalidUser) {
				message = "No pude iniciar el inicio de sesión con Microsoft. Probá de nuevo en un rato."
			}
			return card(cardDanger, message), nil
		}
		return card(cardStyle{Title: "Iniciar sesión con Microsoft", Color: cardInfo.Color}, fmt.Sprintf(
			"1. Abrí %s desde cualquier dispositivo.\n2. Escribí este código: **`%s`**\n3. Entrá con tu cuenta de UADE.\n\nEl código vence a las %s. Te aviso por mensaje directo cuando termine. Nunca te voy a pedir tu contraseña.",
			status.VerificationURI, status.UserCode, status.ExpiresAt.In(argentina).Format("15:04"),
		)), nil
	case "estado":
		status, err := d.Microsoft.Status(userID)
		if err != nil {
			return card(cardDanger, "No pude consultar tu sesión."), nil
		}
		switch status.State {
		case entra.StateActive:
			return card(cardSuccess, "Tu sesión de Microsoft está activa y se renueva sola."), nil
		case entra.StateDead:
			return card(cardWarning, "Tu sesión de Microsoft venció. Usá `/microsoft accion:iniciar` para volver a entrar."), nil
		default:
			return card(cardInfo, "No tenés una sesión de Microsoft. Usá `/microsoft accion:iniciar` para entrar."), nil
		}
	case "cerrar":
		if err := d.Microsoft.Forget(userID); err != nil {
			return card(cardDanger, "No pude cerrar tu sesión."), nil
		}
		return card(cardSuccess, "Cerré tu sesión de Microsoft y borré lo que tenía guardado."), nil
	default:
		return card(cardWarning, "Acción desconocida."), nil
	}
}
