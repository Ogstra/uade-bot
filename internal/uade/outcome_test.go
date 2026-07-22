package uade

import "testing"

func TestClassifyFailClosed(t *testing.T) {
	if got := Classify(false, false, false, nil); got.Code != OutcomeTransientErr {
		t.Fatalf("%q", got.Code)
	}
	if got := Classify(true, true, false, nil); got.Code != OutcomeAuthError {
		t.Fatalf("%q", got.Code)
	}
	if got := Classify(true, false, false, nil); got.Code != OutcomeNoVacancies {
		t.Fatalf("%q", got.Code)
	}
	if got := Classify(true, false, false, []Vacancy{{Codigo: "x"}}); got.Code != OutcomeFound {
		t.Fatalf("%q", got.Code)
	}
}

func TestOutcomeCodesMatchNodeOracle(t *testing.T) {
	want := []OutcomeCode{"found", "no_vacancies", "invalid_credentials", "search_failed"}
	got := []OutcomeCode{OutcomeFound, OutcomeNoVacancies, OutcomeAuthError, OutcomeTransientErr}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("outcome[%d]=%q, want %q", i, got[i], want[i])
		}
	}
}
