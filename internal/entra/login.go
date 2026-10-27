package entra

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"
)

type LoginState string

const (
	LoginIdle    LoginState = "idle"
	LoginPending LoginState = "pending"
	LoginOK      LoginState = "ok"
	LoginFailed  LoginState = "failed"
)

// LoginStatus is everything a UI may show. It never carries a token nor the
// device_code (the secret half of the grant): only the user_code a human types
// and the URL they type it into.
type LoginStatus struct {
	State           LoginState `json:"state"`
	Message         string     `json:"message,omitempty"`
	UserCode        string     `json:"userCode,omitempty"`
	VerificationURI string     `json:"verificationUri,omitempty"`
	ExpiresAt       time.Time  `json:"expiresAt,omitempty"`
}

// Login runs at most one device-code sign-in at a time. Polling Microsoft
// happens here, on the server; a UI only reads Status.
type Login struct {
	Client  *Client
	Session *Session
	Sleep   Sleeper
	Now     func() time.Time
	// OnFinish reports the outcome to whoever started the sign-in. It runs on
	// the polling goroutine once the grant ends, successfully or not.
	OnFinish func(requester string, status LoginStatus)

	mu        sync.Mutex
	current   LoginStatus
	requester string
	cancel    context.CancelFunc
}

func (l *Login) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

func (l *Login) Status() LoginStatus {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.current.State == "" {
		return LoginStatus{State: LoginIdle}
	}
	return l.current
}

// Begin starts a sign-in, or returns the one already pending. Reusing the
// pending code keeps two admins pressing the button at once from racing two
// grants, where whichever completes second would silently replace the first.
func (l *Login) Begin(ctx context.Context, requester string) (LoginStatus, error) {
	l.mu.Lock()
	if l.current.State == LoginPending && l.now().Before(l.current.ExpiresAt) {
		status := l.current
		l.mu.Unlock()
		return status, nil
	}
	l.mu.Unlock()

	dc, err := l.Client.StartDeviceCode(ctx)
	if err != nil {
		status := LoginStatus{State: LoginFailed, Message: summarize(err)}
		l.mu.Lock()
		l.current = status
		l.mu.Unlock()
		return status, err
	}

	// Cancelable, deliberately without a deadline: PollDeviceCode already
	// stops at the code's expiry using the client's clock. A wall-clock
	// deadline here would be a second, disagreeing source of truth for the
	// same instant whenever that clock is not time.Now.
	pollCtx, cancel := context.WithCancel(context.Background())
	status := LoginStatus{
		State:           LoginPending,
		UserCode:        dc.UserCode,
		VerificationURI: dc.VerificationURI,
		ExpiresAt:       dc.ExpiresAt,
	}
	l.mu.Lock()
	if l.cancel != nil {
		l.cancel()
	}
	l.current, l.requester, l.cancel = status, requester, cancel
	l.mu.Unlock()

	go l.poll(pollCtx, cancel, dc, requester)
	return status, nil
}

func (l *Login) poll(ctx context.Context, cancel context.CancelFunc, dc DeviceCode, requester string) {
	defer cancel()
	tokens, err := l.Client.PollDeviceCode(ctx, dc, l.Sleep)
	if err == nil {
		err = l.Session.Adopt(context.Background(), tokens)
	}

	final := LoginStatus{State: LoginOK, Message: "Sesión de Microsoft iniciada."}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			err = &PermanentError{Code: "expired_token"}
		}
		final = LoginStatus{State: LoginFailed, Message: summarize(err)}
		// The code is the only detail kept: an operator needs to know which
		// kind of failure it was, and the code carries no tenant data.
		log.Printf("entra: device-code sign-in failed (%s)", failureKind(err))
	}

	l.mu.Lock()
	// A newer Begin replaced this grant: its outcome is not ours to report.
	if l.current.UserCode != dc.UserCode {
		l.mu.Unlock()
		return
	}
	l.current = final
	l.cancel = nil
	l.mu.Unlock()

	if l.OnFinish != nil {
		l.OnFinish(requester, final)
	}
}

func failureKind(err error) string {
	var p *PermanentError
	if errors.As(err, &p) {
		return p.Code
	}
	var t *TransientError
	if errors.As(err, &t) {
		return "transient"
	}
	return "other"
}

// summarize turns a failure into a fixed sentence. Microsoft's own error
// description is never forwarded: it embeds trace ids, timestamps and
// sometimes the account name.
func summarize(err error) string {
	var p *PermanentError
	if errors.As(err, &p) {
		switch p.Code {
		case "expired_token":
			return "El código venció sin que se completara el inicio de sesión. Pedí uno nuevo."
		case "access_denied", "authorization_declined":
			return "El inicio de sesión fue rechazado desde la cuenta de Microsoft."
		case "invalid_client", "unauthorized_client":
			return "El tenant no permite este cliente de Microsoft (puede estar bloqueado por Conditional Access). Configurá ENTRA_CLIENT_ID con una app propia."
		case "invalid_scope":
			return "El recurso configurado no acepta este permiso. Revisá ENTRA_RESOURCE."
		case "missing_refresh_token":
			return "Microsoft no otorgó acceso sin conexión, así que la sesión no podría renovarse sola."
		case "validation_failed":
			return "Microsoft otorgó la sesión pero el recurso la rechazó. No reemplacé la sesión anterior."
		default:
			return "Microsoft rechazó el inicio de sesión. Pedí un código nuevo."
		}
	}
	var t *TransientError
	if errors.As(err, &t) {
		return "No pude comunicarme con Microsoft. Probá de nuevo en un rato."
	}
	return "No se pudo completar el inicio de sesión."
}
