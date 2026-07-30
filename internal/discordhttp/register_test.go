package discordhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
)

func TestRegisterCommandCleanupConvergesGlobalThenSortedGuilds(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	var bodies [][]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("method=%s", r.Method)
		}
		var body []any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		paths = append(paths, r.URL.Path)
		bodies = append(bodies, body)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	if err := ConvergeCommands(context.Background(), server.Client(), server.URL, "token", "app", []string{"g2", "g1", "g2", ""}); err != nil {
		t.Fatal(err)
	}
	want := []string{"/applications/app/commands", "/applications/app/guilds/g1/commands", "/applications/app/guilds/g2/commands"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths=%v want=%v", paths, want)
	}
	if len(bodies[0]) != 12 || len(bodies[1]) != 0 || len(bodies[2]) != 0 {
		t.Fatalf("body sizes=%d,%d,%d", len(bodies[0]), len(bodies[1]), len(bodies[2]))
	}
}

func TestRegisterCommandCleanupDoesNotDeleteGuildsWhenGlobalFails(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		http.Error(w, "global failed with sensitive-looking body", http.StatusBadGateway)
	}))
	defer server.Close()
	err := ConvergeCommands(context.Background(), server.Client(), server.URL, "token", "app", []string{"g1"})
	if err == nil {
		t.Fatal("expected global error")
	}
	if !reflect.DeepEqual(paths, []string{"/applications/app/commands"}) {
		t.Fatalf("paths=%v", paths)
	}
}

func TestRegisterCommandCleanupRetriesGuildRateLimit(t *testing.T) {
	calls := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls[r.URL.Path]++
		if r.URL.Path == "/applications/app/guilds/g1/commands" && calls[r.URL.Path] == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	if err := ConvergeCommands(context.Background(), server.Client(), server.URL, "token", "app", []string{"g1"}); err != nil {
		t.Fatal(err)
	}
	if calls["/applications/app/guilds/g1/commands"] != 2 {
		t.Fatalf("guild calls=%d", calls["/applications/app/guilds/g1/commands"])
	}
}
