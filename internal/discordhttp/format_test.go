package discordhttp

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestFormatFilterValidDiasKeepsOnlyWhitelistedDaysUppercasedAndTrimmed(t *testing.T) {
	got := filterValidDias(" lu, XX, mi , JU,ju,ZZ")
	want := []string{"LU", "MI", "JU", "JU"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i, dia := range want {
		if got[i] != dia {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestFormatFilterValidDiasRejectsAllInvalid(t *testing.T) {
	if got := filterValidDias("XX,YY,ZZ"); len(got) != 0 {
		t.Fatalf("got %v, want empty", got)
	}
}

func TestFormatJobStatusText(t *testing.T) {
	active := sql.NullString{}
	pausedAccount := sql.NullString{String: "needs_credentials", Valid: true}

	cases := []struct {
		name        string
		jobStatus   string
		pauseReason sql.NullString
		want        string
	}{
		{"job activo, cuenta activa", "active", active, "activa"},
		{"job pausado, cuenta activa", "paused_by_user", active, "pausada"},
		{"job activo, cuenta pausada con motivo", "active", pausedAccount, "pausada (needs_credentials)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatJobStatusText(tc.jobStatus, tc.pauseReason); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFormatJobLocation(t *testing.T) {
	valid := func(v string) sql.NullString { return sql.NullString{String: v, Valid: true} }
	empty := sql.NullString{}

	cases := []struct {
		name             string
		guildID, channel sql.NullString
		want             string
	}{
		{"sin guild, con canal -> DM", empty, valid("chan1"), "DM"},
		{"sin guild, sin canal -> sin canal", empty, empty, "sin canal"},
		{"con guild y canal -> mencion", valid("guild1"), valid("chan1"), "<#chan1>"},
		{"con guild, sin canal -> servidor id", valid("guild1"), empty, "servidor guild1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatJobLocation(tc.guildID, tc.channel); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFormatLastPoll(t *testing.T) {
	if got := formatLastPoll(sql.NullInt64{}); got != "sin sondeos" {
		t.Fatalf("got %q", got)
	}
	fixed := time.Date(2026, 7, 30, 15, 4, 5, 0, time.Local)
	got := formatLastPoll(sql.NullInt64{Int64: fixed.UnixMilli(), Valid: true})
	want := fixed.Format("02/01/2006 15:04:05")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestFormatTruncateAdminListNoTruncationUnderLimit(t *testing.T) {
	lines := []string{"linea uno", "linea dos", "linea tres"}
	got := truncateAdminList(lines)
	want := strings.Join(lines, "\n")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if strings.Contains(got, "...y") {
		t.Fatalf("no debería truncar: %q", got)
	}
}

func TestFormatTruncateAdminListForcesCutoffOverLimit(t *testing.T) {
	lines := make([]string, 0, 200)
	for i := 0; i < 200; i++ {
		lines = append(lines, "**#"+strconv.Itoa(i)+"** <@user"+strconv.Itoa(i)+"> · materia · Noche LU/MA · activa · <#chan> · sin resultado")
	}
	got := truncateAdminList(lines)
	if len(got) > 1900 {
		t.Fatalf("resultado excede 1900 caracteres: %d", len(got))
	}
	idx := strings.Index(got, "\n_...y ")
	if idx == -1 {
		t.Fatalf("falta sufijo de truncamiento: %q", got)
	}
	suffix := got[idx+len("\n_...y "):]
	spaceIdx := strings.Index(suffix, " mas")
	if spaceIdx == -1 {
		t.Fatalf("sufijo mal formado: %q", suffix)
	}
	omittedCount, err := strconv.Atoi(suffix[:spaceIdx])
	if err != nil {
		t.Fatalf("no pude parsear cuenta omitida: %v", err)
	}
	// Confirmar que las líneas mostradas + las omitidas suman el total.
	shownLines := strings.Count(got[:idx], "\n") + 1
	if shownLines+omittedCount != len(lines) {
		t.Fatalf("shownLines=%d + omitidas=%d != total=%d", shownLines, omittedCount, len(lines))
	}
}

func TestFormatFilterSummaryBlock(t *testing.T) {
	got := filterSummaryBlock(
		"Algoritmos nocturna",
		"3.1.050",
		"Algoritmos y Estructuras de Datos",
		"Noche",
		"curricular",
		[]string{"LU", "MI"},
		[]string{"Lima", "Monserrat"},
	)
	want := "**Busqueda creada:** Algoritmos nocturna\n" +
		"**Materia:** `3.1.050` - Algoritmos y Estructuras de Datos\n" +
		"**Turno:** Noche\n" +
		"**Ofrecimiento:** curricular\n" +
		"**Dias:** LU, MI\n" +
		"**Sedes excluidas:** Lima, Monserrat"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}

	withoutOptional := filterSummaryBlock("3.1.050", "3.1.050", "", "Tarde", "intensiva", []string{"SA"}, nil)
	if strings.Contains(withoutOptional, "Sedes excluidas") || strings.Contains(withoutOptional, " - \n") {
		t.Fatalf("campos opcionales inesperados: %s", withoutOptional)
	}
}
