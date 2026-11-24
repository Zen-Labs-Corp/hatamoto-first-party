package core_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core/coretest"
)

func settingsHost(t *testing.T, res any, err error) {
	coretest.Install(t, &coretest.Host{Fns: map[string]func(json.RawMessage) (any, error){
		"settings.get": func(json.RawMessage) (any, error) { return res, err },
	}})
}

func TestSettingsDefaultsOnHostError(t *testing.T) {
	settingsHost(t, nil, errors.New("down"))
	if got := core.LoadSettings(); got != core.DefaultSettings() {
		t.Errorf("got %+v", got)
	}
}

func TestSettingsPartialOverrideAndMinimums(t *testing.T) {
	settingsHost(t, map[string]any{"flag_karma": 0, "page_size": 10, "flags_to_dead": 0, "submit_per_hour": -1}, nil)
	got := core.LoadSettings()
	want := core.DefaultSettings()
	want.FlagKarma = 0 // zero is legal for flag_karma
	want.PageSize = 10
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestSettingsPageSizeClamp(t *testing.T) {
	settingsHost(t, map[string]any{"page_size": 5000}, nil)
	if got := core.LoadSettings().PageSize; got != 100 {
		t.Errorf("PageSize %d", got)
	}
	settingsHost(t, map[string]any{"page_size": 0}, nil)
	if got := core.LoadSettings().PageSize; got != 30 {
		t.Errorf("PageSize %d", got)
	}
}
