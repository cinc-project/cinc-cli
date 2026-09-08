package explore

import (
	"bytes"
	"context"
	"strings"
	"testing"

	cinc "github.com/cinc-project/cinc-api"

	"github.com/cinc-project/cinc-cli/cli/config"
)

// stubClient is a NewClient that hands back a non-nil client without
// touching the network, recording how many times it was called.
func stubClient(calls *int) func(config.Profile) (*cinc.Client, error) {
	return func(config.Profile) (*cinc.Client, error) {
		*calls++
		return &cinc.Client{}, nil
	}
}

func TestResolveStartupSingleProfileSkipsPicker(t *testing.T) {
	var calls int
	s, err := resolveStartup(Options{
		Profiles:  map[string]config.Profile{"default": {}},
		NewClient: stubClient(&calls),
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.screen != screenKinds {
		t.Errorf("screen = %v, want kinds", s.screen)
	}
	if s.client == nil || s.profileName != "default" || calls != 1 {
		t.Errorf("expected a built client for the sole profile (calls=%d)", calls)
	}
}

func TestResolveStartupPreselectedSkipsPicker(t *testing.T) {
	var calls int
	s, err := resolveStartup(Options{
		Profiles:    map[string]config.Profile{"prod": {}, "dev": {}},
		Preselected: "prod",
		NewClient:   stubClient(&calls),
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.screen != screenKinds || s.profileName != "prod" {
		t.Errorf("startup = %+v, want kinds/prod", s)
	}
}

func TestResolveStartupMultipleShowsPicker(t *testing.T) {
	var calls int
	s, err := resolveStartup(Options{
		Profiles:  map[string]config.Profile{"prod": {}, "dev": {}},
		NewClient: stubClient(&calls),
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.screen != screenProfiles {
		t.Errorf("screen = %v, want profiles", s.screen)
	}
	if s.client != nil || calls != 0 {
		t.Errorf("picker must defer client construction (calls=%d)", calls)
	}
	if !equal(s.profileNames, []string{"dev", "prod"}) {
		t.Errorf("profileNames = %v, want sorted dev,prod", s.profileNames)
	}
}

func TestResolveStartupUnknownPreselectedErrors(t *testing.T) {
	var calls int
	_, err := resolveStartup(Options{
		Profiles:    map[string]config.Profile{"default": {}},
		Preselected: "ghost",
		NewClient:   stubClient(&calls),
	})
	if err == nil {
		t.Fatal("expected an error for an unknown preselected profile")
	}
}

func TestRunRejectsNonTTY(t *testing.T) {
	var calls int
	err := Run(context.Background(), Options{
		Profiles:  map[string]config.Profile{"default": {}},
		NewClient: stubClient(&calls),
		Stdin:     strings.NewReader(""),
		Stdout:    &bytes.Buffer{}, // not a *os.File → not a TTY
	})
	if err == nil || !strings.Contains(err.Error(), "interactive terminal") {
		t.Fatalf("err = %v, want an interactive-terminal message", err)
	}
}
