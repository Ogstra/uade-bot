package sso

// See relink_test.go's header comment for why some subtests here run a
// second httptest server addressed via "localhost" instead of the default
// "127.0.0.1" -- it's the only way to get two distinct Hostname() values
// out of local httptest servers, which is what allowedHosts/CheckRedirect
// actually compare against.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewClientFollowsRedirectToAllowedHost(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "landed")
	}))
	defer target.Close()
	targetBase := localhostURL(target.URL)
	setMicrosoftLoginHost(t, hostnameOf(targetBase))

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, targetBase+"/next", http.StatusFound)
	}))
	defer source.Close()

	client, err := NewClient(source.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	resp, err := client.Get(source.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestNewClientRejectsRedirectToDisallowedHost(t *testing.T) {
	evilHit := false
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		evilHit = true
	}))
	defer evil.Close()

	// evil must resolve to a hostname distinct from source's -- both default
	// to "127.0.0.1" on different ports otherwise, which allowedHosts (a
	// Hostname()-only comparison) would treat as the same, allowed host.
	evilBase := localhostURL(evil.URL)
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, evilBase+"/next", http.StatusFound)
	}))
	defer source.Close()

	client, err := NewClient(source.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := client.Get(source.URL); err == nil {
		t.Fatal("expected error for redirect to a disallowed host")
	}
	if evilHit {
		t.Fatal("the disallowed host received a request")
	}
}

func TestNewClientBoundsRedirectChainLength(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hop := 0
		if raw := r.URL.Query().Get("hop"); raw != "" {
			fmt.Sscanf(raw, "%d", &hop)
		}
		http.Redirect(w, r, fmt.Sprintf("%s/?hop=%d", srv.URL, hop+1), http.StatusFound)
	}))
	defer srv.Close()

	client, err := NewClient(srv.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := client.Get(srv.URL); err == nil {
		t.Fatal("expected error for a redirect chain longer than 8 hops")
	}
}

func TestBoundedFetcherEnforcesBodyLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", maxSSOBodyBytes+1)))
	}))
	defer srv.Close()

	client, err := NewClient(srv.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	fetcher, err := newBoundedFetcher(client, srv.URL)
	if err != nil {
		t.Fatalf("newBoundedFetcher: %v", err)
	}
	if _, _, err := fetcher.get(context.Background(), srv.URL); err == nil {
		t.Fatal("expected error for a response body over maxSSOBodyBytes")
	}
}

func TestNewBoundedFetcherRequiresClient(t *testing.T) {
	if _, err := newBoundedFetcher(nil, "https://example.com"); err == nil {
		t.Fatal("expected error for a nil http.Client")
	}
}
