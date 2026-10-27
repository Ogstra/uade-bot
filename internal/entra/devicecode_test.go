package entra

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

const deviceCodeJSON = `{"device_code":"DEV-SECRET","user_code":"ABCD-EFGH","verification_uri":"https://microsoft.com/devicelogin","expires_in":900,"interval":5}`

func TestStartDeviceCodeRequestsOfflineAccessOnTheResourceRoot(t *testing.T) {
	fake := newScripted().on("/devicecode", reply{status: 200, body: deviceCodeJSON})
	clk := newClock()
	client := &Client{Config: testConfig(), HTTP: fake, Now: clk.Now}

	dc, err := client.StartDeviceCode(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if dc.UserCode != "ABCD-EFGH" || dc.VerificationURI != "https://microsoft.com/devicelogin" {
		t.Fatalf("device code = %+v", dc)
	}
	if !dc.ExpiresAt.Equal(clk.Now().Add(900 * time.Second)) {
		t.Fatalf("ExpiresAt = %v", dc.ExpiresAt)
	}
	req := fake.last("/devicecode")
	if req.path != "/tenant-1/oauth2/v2.0/devicecode" {
		t.Fatalf("path = %q", req.path)
	}
	if got := req.form.Get("scope"); got != "https://contoso.sharepoint.com/.default offline_access" {
		t.Fatalf("scope = %q: without offline_access there is no refresh token", got)
	}
	if req.form.Get("client_id") != OfficeClientID {
		t.Fatalf("client_id = %q, want the Office default when none is configured", req.form.Get("client_id"))
	}
}

func TestConfiguredClientIDOverridesTheOfficeDefault(t *testing.T) {
	fake := newScripted().on("/devicecode", reply{status: 200, body: deviceCodeJSON})
	cfg := testConfig()
	cfg.ClientID = "own-app-id"
	client := &Client{Config: cfg, HTTP: fake}

	if _, err := client.StartDeviceCode(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := fake.last("/devicecode").form.Get("client_id"); got != "own-app-id" {
		t.Fatalf("client_id = %q, want own-app-id", got)
	}
}

// pending -> slow_down -> pending -> ok. slow_down must add 5s to the
// interval for the rest of the grant, not just for one wait.
func TestPollingWaitsThroughPendingAndBacksOffOnSlowDown(t *testing.T) {
	fake := newScripted().on("/token",
		reply{status: 400, body: errJSON("authorization_pending")},
		reply{status: 400, body: errJSON("slow_down")},
		reply{status: 400, body: errJSON("authorization_pending")},
		reply{status: 200, body: tokenJSON("AT-1", "RT-1", 3600)},
	)
	clk := newClock()
	sl := &sleeps{clock: clk}
	client := &Client{Config: testConfig(), HTTP: fake, Now: clk.Now}
	dc := DeviceCode{DeviceCode: "DEV-SECRET", ExpiresAt: clk.Now().Add(15 * time.Minute), Interval: 5 * time.Second}

	tokens, err := client.PollDeviceCode(context.Background(), dc, sl.Sleep)
	if err != nil {
		t.Fatal(err)
	}

	if tokens.AccessToken != "AT-1" || tokens.RefreshToken != "RT-1" {
		t.Fatalf("tokens = %+v", tokens)
	}
	want := []time.Duration{5 * time.Second, 5 * time.Second, 10 * time.Second, 10 * time.Second}
	if len(sl.got) != len(want) {
		t.Fatalf("waits = %v, want %v", sl.got, want)
	}
	for i := range want {
		if sl.got[i] != want[i] {
			t.Fatalf("waits = %v, want %v", sl.got, want)
		}
	}
	req := fake.last("/token")
	if req.form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" || req.form.Get("device_code") != "DEV-SECRET" {
		t.Fatalf("token request form = %v", req.form)
	}
}

func TestPollingStopsForGoodOnTerminalErrors(t *testing.T) {
	for _, code := range []string{"expired_token", "access_denied", "invalid_grant"} {
		t.Run(code, func(t *testing.T) {
			fake := newScripted().on("/token", reply{status: 400, body: errJSON(code)})
			clk := newClock()
			client := &Client{Config: testConfig(), HTTP: fake, Now: clk.Now}
			dc := DeviceCode{DeviceCode: "D", ExpiresAt: clk.Now().Add(time.Hour), Interval: time.Second}

			_, err := client.PollDeviceCode(context.Background(), dc, (&sleeps{clock: clk}).Sleep)

			var p *PermanentError
			if !errors.As(err, &p) || p.Code != code {
				t.Fatalf("err = %v, want PermanentError(%s)", err, code)
			}
			if fake.count("/token") != 1 {
				t.Fatalf("token calls = %d, want 1: a terminal error must not be retried", fake.count("/token"))
			}
		})
	}
}

// Microsoft's expires_in is the authority, but the server also stops on its
// own clock: a grant past its expiry is not polled even if Microsoft never
// says expired_token.
func TestPollingGivesUpAtTheCodeExpiryOnItsOwn(t *testing.T) {
	fake := newScripted().on("/token", reply{status: 400, body: errJSON("authorization_pending")})
	clk := newClock()
	client := &Client{Config: testConfig(), HTTP: fake, Now: clk.Now}
	dc := DeviceCode{DeviceCode: "D", ExpiresAt: clk.Now().Add(12 * time.Second), Interval: 5 * time.Second}

	_, err := client.PollDeviceCode(context.Background(), dc, (&sleeps{clock: clk}).Sleep)

	var p *PermanentError
	if !errors.As(err, &p) || p.Code != "expired_token" {
		t.Fatalf("err = %v, want expired_token", err)
	}
}

// A network blip while the user is typing their code must not abort the grant.
func TestPollingSurvivesTransientFailures(t *testing.T) {
	fake := newScripted().on("/token",
		reply{err: errors.New("connection reset")},
		reply{status: 503, body: `upstream unavailable`},
		reply{status: 200, body: tokenJSON("AT", "RT", 3600)},
	)
	clk := newClock()
	client := &Client{Config: testConfig(), HTTP: fake, Now: clk.Now}
	dc := DeviceCode{DeviceCode: "D", ExpiresAt: clk.Now().Add(time.Hour), Interval: time.Second}

	tokens, err := client.PollDeviceCode(context.Background(), dc, (&sleeps{clock: clk}).Sleep)
	if err != nil || tokens.AccessToken != "AT" {
		t.Fatalf("tokens=%+v err=%v", tokens, err)
	}
}

// Error strings reach logs. They may carry the OAuth code, never Microsoft's
// description (trace ids, account names).
func TestErrorsNeverCarryMicrosoftsDescription(t *testing.T) {
	fake := newScripted().on("/token", reply{status: 400, body: errJSON("invalid_grant")})
	client := &Client{Config: testConfig(), HTTP: fake}

	_, err := client.Refresh(context.Background(), "RT")

	for _, leak := range []string{"AADSTS", "secret-trace-id", "user@uade.edu.ar"} {
		if strings.Contains(err.Error(), leak) {
			t.Fatalf("error %q leaks %q", err.Error(), leak)
		}
	}
}

func TestConfigRejectsAResourceWithAPath(t *testing.T) {
	cfg := testConfig()
	cfg.Resource = "https://contoso.sharepoint.com/sites/x"
	if cfg.Validate() == nil {
		t.Fatal("resource with a path accepted: the scope must be the tenant root")
	}
	cfg.Resource = "http://contoso.sharepoint.com"
	if cfg.Validate() == nil {
		t.Fatal("plain-http resource accepted")
	}
}
