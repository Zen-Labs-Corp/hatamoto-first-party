package core

// Settings are the plugin settings (plugin.json settings_schema).
type Settings struct {
	FlagKarma         int `json:"flag_karma"`
	DownvoteKarma     int `json:"downvote_karma"`
	VouchKarma        int `json:"vouch_karma"`
	EditWindowMinutes int `json:"edit_window_minutes"`
	SubmitPerHour     int `json:"submit_per_hour"`
	FlagsToDead       int `json:"flags_to_dead"`
	PageSize          int `json:"page_size"`
}

// DefaultSettings equals the defaults in plugin.json.
func DefaultSettings() Settings {
	return Settings{FlagKarma: 30, DownvoteKarma: 500, VouchKarma: 31, EditWindowMinutes: 120, SubmitPerHour: 6, FlagsToDead: 5, PageSize: 30}
}

// LoadSettings reads the settings on every call (no cache). Any host error, and
// any value below its minimum, falls back to the default; PageSize is clamped to 1..100.
func LoadSettings() Settings {
	def := DefaultSettings()
	if Call == nil {
		return def
	}
	// Decode over a pointer-valued shape so an absent key is told from a zero.
	var raw struct {
		FlagKarma         *int `json:"flag_karma"`
		DownvoteKarma     *int `json:"downvote_karma"`
		VouchKarma        *int `json:"vouch_karma"`
		EditWindowMinutes *int `json:"edit_window_minutes"`
		SubmitPerHour     *int `json:"submit_per_hour"`
		FlagsToDead       *int `json:"flags_to_dead"`
		PageSize          *int `json:"page_size"`
	}
	if err := Call("settings.get", struct{}{}, &raw); err != nil {
		return def
	}
	pick := func(v *int, def, min int) int {
		if v == nil || *v < min {
			return def
		}
		return *v
	}
	s := Settings{
		FlagKarma:         pick(raw.FlagKarma, def.FlagKarma, 0),
		DownvoteKarma:     pick(raw.DownvoteKarma, def.DownvoteKarma, 0),
		VouchKarma:        pick(raw.VouchKarma, def.VouchKarma, 0),
		EditWindowMinutes: pick(raw.EditWindowMinutes, def.EditWindowMinutes, 0),
		SubmitPerHour:     pick(raw.SubmitPerHour, def.SubmitPerHour, 0),
		FlagsToDead:       pick(raw.FlagsToDead, def.FlagsToDead, 1),
		PageSize:          pick(raw.PageSize, def.PageSize, 1),
	}
	if s.PageSize > 100 {
		s.PageSize = 100
	}
	return s
}
