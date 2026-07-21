package uade

import (
	"os"
	"strings"
	"testing"
)

func TestParseDeltaFixture(t *testing.T) {
	b, e := os.ReadFile("../../src/automation/__fixtures__/webforms/delta-found.txt")
	if e != nil {
		t.Fatal(e)
	}
	r, f := ParseDelta(strings.TrimSpace(string(b)), 100, 1_000_000)
	if f != "" || len(r.Panels) != 1 || len(r.Hidden) != 1 {
		t.Fatalf("%+v %q", r, f)
	}
}
func TestParseDeltaRejectsMalformed(t *testing.T) {
	_, f := ParseDelta("1|updatePanel|x|a", 100, 100)
	if f != "delta_missing_delimiter" && f != "delta_truncated" {
		t.Fatalf("%q", f)
	}
}
