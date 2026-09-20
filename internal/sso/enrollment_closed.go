package sso

import "errors"

// ErrEnrollmentClosed marks a relink failure caused by UADE not running an
// enrollment period, as opposed to anything wrong with this user's account.
// Relink reports it alongside ErrInvalidStartURL (see startURLError.Unwrap),
// so callers that only care that no start URL came back keep working
// untouched while the poll path can branch on the cause.
var ErrEnrollmentClosed = errors.New("uade is not running an enrollment period")

// EnrollmentClosed answers the question the bare ErrInvalidStartURL sentinel
// cannot: did Relink fail because THIS user's link is unusable, or because
// UADE is simply not running an enrollment period right now?
//
// The two look identical from the poll's side -- no usable start URL either
// way -- but the correct response is opposite. A stale link is the user's to
// fix (/credenciales, re-auth) and warrants a DM. A closed period is nobody's
// to fix: telling every user to relink during the months between enrollment
// windows is the exact loop this classifier exists to break.
//
// The discriminator is the one hypothesis (A) that start_url_diagnostics.go
// already isolates: the Asignaturas panel is present but holds no
// InscripcionAsignatura link. Panel present means the page rendered and the
// selectors still match, so the empty link set is UADE's own state and not a
// scraping regression. A missing panel (hypothesis C) deliberately does NOT
// count -- that is structural breakage, and reporting it to users as "closed"
// would hide a real bug behind a reassuring message until someone noticed the
// bot had been silent for a whole enrollment period.
func EnrollmentClosed(err error) bool {
	return errors.Is(err, ErrEnrollmentClosed)
}
