package shadow

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ogs/uade-bot/internal/uade"
	_ "modernc.org/sqlite"
)

func TestComparatorUsesEquivalentCopiedInputAndSanitizesDivergence(t *testing.T) {
	secret := []byte("password=operative-secret")
	now := time.Unix(0, 0)
	clock := func() time.Time { now = now.Add(time.Millisecond); return now }
	rss := uint64(100)
	seen := 0
	comparator := &Comparator{
		Now: clock, RSS: func() uint64 { rss += 10; return rss },
		Node: RunnerFunc(func(_ context.Context, input Input) (Result, error) {
			seen++
			input.Payload[0] = 'X'
			return Result{Outcome: uade.Outcome{Code: uade.OutcomeFound, Vacancies: []uade.Vacancy{{Codigo: "A", Cupos: 1}}}, RSSBytes: 50_000_000}, nil
		}),
		Go: RunnerFunc(func(_ context.Context, input Input) (Result, error) {
			seen++
			if string(input.Payload) != string(secret) {
				t.Fatalf("runner inputs differ: %q", input.Payload)
			}
			return Result{Outcome: uade.Outcome{Code: uade.OutcomeNoVacancies}, RSSBytes: 10_000_000}, nil
		}),
	}
	observation, err := comparator.Compare(context.Background(), Input{FixtureID: "fixture-1", Payload: secret})
	if err != nil || seen != 2 || observation.Divergence == nil || !observation.Divergence.Critical {
		t.Fatalf("observation=%+v err=%v", observation, err)
	}
	encoded, _ := comparator.Report(time.Unix(0, 0), time.Unix(60, 0)).MarshalForTest()
	if strings.Contains(string(encoded), "operative-secret") || observation.Metrics.GoRSSBytes == 0 || observation.Metrics.NodeLatencyMicros == 0 {
		t.Fatalf("unsanitized or missing metrics: %s", encoded)
	}
}

func (r Report) MarshalForTest() ([]byte, error) {
	path := filepath.Join(os.TempDir(), "uade-shadow-test.json")
	if err := r.Write(path); err != nil {
		return nil, err
	}
	defer os.Remove(path)
	return os.ReadFile(path)
}

func TestReadOnlyDatabaseRejectsWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shadow.db")
	writable, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = writable.Exec("CREATE TABLE evidence (id INTEGER)"); err != nil {
		t.Fatal(err)
	}
	writable.Close()

	readOnly, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer readOnly.Close()
	if _, err = readOnly.Exec("INSERT INTO evidence VALUES (1)"); err == nil {
		t.Fatal("shadow DB accepted a write")
	}
}

func TestReadinessFailsClosedOnEveryCutoverGate(t *testing.T) {
	report := Report{StartedAt: time.Unix(0, 0), EndedAt: time.Unix(30, 0), Comparisons: 9, CriticalDivergences: 1}
	err := (Readiness{MinimumWindow: time.Minute, MinimumComparisons: 10}).Evaluate(report)
	for _, expected := range []string{"GO-09", "Discord", "divergences", "minimum", "window"} {
		if err == nil || !strings.Contains(err.Error(), expected) {
			t.Fatalf("missing blocker %q in %v", expected, err)
		}
	}
	ready := Readiness{MinimumWindow: time.Minute, MinimumComparisons: 10, GO09Checkpoint: true, DiscordLive: true}
	report = Report{StartedAt: time.Unix(0, 0), EndedAt: time.Unix(61, 0), Comparisons: 10}
	if err = ready.Evaluate(report); err != nil {
		t.Fatal(err)
	}
}

func TestShadowRSSFallbackNeverAttributesGoProcessMemoryToNodeSide(t *testing.T) {
	comparator := &Comparator{
		RSS: func() uint64 { return 42_000_000 },
		Node: RunnerFunc(func(context.Context, Input) (Result, error) {
			return Result{Outcome: uade.Outcome{Code: uade.OutcomeNoVacancies}, RSSBytes: 0}, nil
		}),
		Go: RunnerFunc(func(context.Context, Input) (Result, error) {
			return Result{Outcome: uade.Outcome{Code: uade.OutcomeNoVacancies}, RSSBytes: 0}, nil
		}),
	}
	observation, err := comparator.Compare(context.Background(), Input{FixtureID: "fixture-2", Payload: []byte("payload")})
	if err != nil {
		t.Fatal(err)
	}
	if observation.Metrics.NodeRSSBytes != 0 || observation.Metrics.NodeRSSMeasured {
		t.Fatalf("expected unmeasured Node RSS, got NodeRSSBytes=%d NodeRSSMeasured=%v", observation.Metrics.NodeRSSBytes, observation.Metrics.NodeRSSMeasured)
	}
	if observation.Metrics.GoRSSBytes == 0 || !observation.Metrics.GoRSSMeasured {
		t.Fatalf("expected measured Go RSS via same-process fallback, got GoRSSBytes=%d GoRSSMeasured=%v", observation.Metrics.GoRSSBytes, observation.Metrics.GoRSSMeasured)
	}
}

func BenchmarkComparator(b *testing.B) {
	outcome := uade.Outcome{Code: uade.OutcomeFound, Vacancies: []uade.Vacancy{{Codigo: "31.001", Turno: "Noche", Sede: "Monserrat", Cupos: 2}}}
	runner := RunnerFunc(func(context.Context, Input) (Result, error) { return Result{Outcome: outcome}, nil })
	comparator := &Comparator{Node: runner, Go: runner}
	input := Input{FixtureID: "benchmark", Payload: []byte(`{"materia":"31.001","turno":"Noche"}`)}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := comparator.Compare(context.Background(), input); err != nil {
			b.Fatal(err)
		}
	}
	report := comparator.Report(time.Unix(0, 0), time.Now())
	b.ReportMetric(float64(report.Metrics[len(report.Metrics)-1].GoRSSBytes), "go-rss-bytes")
	b.ReportMetric(float64(report.Metrics[len(report.Metrics)-1].NodeRSSBytes), "node-rss-bytes")
}
