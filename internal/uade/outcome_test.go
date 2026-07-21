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
