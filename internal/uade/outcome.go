package uade

// OutcomeCode is deliberately finite: callers must not treat an ambiguous
// response as an empty result.
type OutcomeCode string

const (
	OutcomeFound        OutcomeCode = "found"
	OutcomeNoVacancies  OutcomeCode = "no_vacancies"
	OutcomeAuthError    OutcomeCode = "invalid_credentials"
	OutcomeTransientErr OutcomeCode = "search_failed"
)

type Vacancy struct {
	Codigo, Materia, Turno, Sede, Horario, Dias string
	Cupos                                       int
}
type Outcome struct {
	Code      OutcomeCode
	Vacancies []Vacancy
	Reason    string
}

func Classify(searchVerified bool, authFailed bool, transportFailed bool, vacancies []Vacancy) Outcome {
	if authFailed {
		return Outcome{Code: OutcomeAuthError, Reason: "credentials rejected"}
	}
	if transportFailed || !searchVerified {
		return Outcome{Code: OutcomeTransientErr, Reason: "search response not verified"}
	}
	if len(vacancies) == 0 {
		return Outcome{Code: OutcomeNoVacancies}
	}
	return Outcome{Code: OutcomeFound, Vacancies: vacancies}
}
