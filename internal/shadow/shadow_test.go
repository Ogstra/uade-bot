package shadow

import (
	"github.com/ogs/uade-bot/internal/uade"
	"testing"
)

func TestComparatorBlocksCriticalDivergence(t *testing.T) {
	c := Comparator{}
	c.Compare(uade.Outcome{Code: uade.OutcomeFound}, uade.Outcome{Code: uade.OutcomeNoVacancies})
	if c.Ready() {
		t.Fatal("must block cutover")
	}
}
