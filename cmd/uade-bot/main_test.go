package main

import (
	"testing"

	"github.com/Ogstra/uade-bot/internal/discordgateway"
	"github.com/Ogstra/uade-bot/internal/discordhttp"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
)

// TestResolveSSOPortalURLDefaultsWhenUnset covers the "UADE_SSO_PORTAL_URL
// unset -> default host, starts normally" behavior from 03.3-15-PLAN.md.
func TestResolveSSOPortalURLDefaultsWhenUnset(t *testing.T) {
	got, err := resolveSSOPortalURL("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "https://inscripciones.uade.edu.ar/" {
		t.Fatalf("got %q", got)
	}
}

// TestResolveSSOPortalURLAcceptsConfiguredHostCaseInsensitively covers an
// explicitly-set, correctly-hosted UADE_SSO_PORTAL_URL passing through
// unchanged regardless of host casing.
func TestResolveSSOPortalURLAcceptsConfiguredHostCaseInsensitively(t *testing.T) {
	got, err := resolveSSOPortalURL("https://INSCRIPCIONES.uade.edu.ar/entry")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "https://INSCRIPCIONES.uade.edu.ar/entry" {
		t.Fatalf("got %q", got)
	}
}

// TestResolveSSOPortalURLRejectsMismatchedHost covers the fail-closed
// behavior: a UADE_SSO_PORTAL_URL pointing at a different host must fail
// before the caller (main) ever reaches app.NewRuntime -- never send
// credentials to an arbitrary host (T-03.3-15-01).
func TestResolveSSOPortalURLRejectsMismatchedHost(t *testing.T) {
	if _, err := resolveSSOPortalURL("https://evil.example.com/"); err == nil {
		t.Fatal("expected error for mismatched host")
	}
}

// TestResolveSSOPortalURLRejectsUnparsableURL guards against a malformed
// value (e.g. containing a raw control character) silently falling through.
func TestResolveSSOPortalURLRejectsUnparsableURL(t *testing.T) {
	if _, err := resolveSSOPortalURL("https://%zz/"); err == nil {
		t.Fatal("expected error for unparsable url")
	}
}

func TestDashboardWiringUsesLiveGatewayProjection(t *testing.T) {
	projection := discordgateway.NewProjection(nil)
	source := dashboardSnapshotSource(nil, "fake/db/path", projection)
	if source.GuildProvider == nil || source.DisplayNameProvider == nil || source.AvatarURLProvider == nil {
		t.Fatalf("dashboard snapshot source is not wired to live gateway providers: %+v", source)
	}
	if source.DBPath != "fake/db/path" {
		t.Fatalf("DBPath=%q", source.DBPath)
	}
}

func TestDispatcherWiringUsesLiveGatewayProjection(t *testing.T) {
	projection := discordgateway.NewProjection(nil)
	dispatcher := wireGatewayIdentity(discordhttp.CommandDispatcher{}, projection)
	if dispatcher.IdentityResolver == nil {
		t.Fatal("dispatcher identity resolver is nil")
	}
	user := discord.User{ID: snowflake.ID(9), Username: "username"}
	projection.Observe(snowflake.ID(7), nil, user)
	if got := dispatcher.IdentityResolver.ResolveIdentity("7", "9"); got != "username" {
		t.Fatalf("initial identity=%q", got)
	}
	late := "late nickname"
	projection.Observe(snowflake.ID(7), &discord.ResolvedMember{Member: discord.Member{Nick: &late}}, user)
	if got := dispatcher.IdentityResolver.ResolveIdentity("7", "9"); got != late {
		t.Fatalf("resolver captured snapshot: %q", got)
	}
}
