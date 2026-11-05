package main

import "strings"

// The chat platforms this plugin can post to.
const (
	chanSlack    = "slack"
	chanTeams    = "teams"
	chanGChat    = "gchat"
	chanTelegram = "telegram"
)

// cfg is the saved settings. Secrets arrive unmasked from settings.get.
type cfg struct {
	Forms            []string `json:"forms"`
	Fields           []string `json:"fields"`
	IncludeValues    *bool    `json:"include_values"`
	MaxValueLength   int      `json:"max_value_length"`
	AdminURL         string   `json:"admin_url"`
	SlackEnabled     bool     `json:"slack_enabled"`
	SlackWebhookURL  string   `json:"slack_webhook_url"`
	TeamsEnabled     bool     `json:"teams_enabled"`
	TeamsWorkflowURL string   `json:"teams_workflow_url"`
	GChatEnabled     bool     `json:"gchat_enabled"`
	GChatWebhookURL  string   `json:"gchat_webhook_url"`
	TelegramEnabled  bool     `json:"telegram_enabled"`
	TelegramBotToken string   `json:"telegram_bot_token"`
	TelegramChatID   string   `json:"telegram_chat_id"`
}

func loadCfg() (cfg, error) {
	var c cfg
	if err := callHost("settings.get", struct{}{}, &c); err != nil {
		return cfg{}, err
	}
	return c, nil
}

// watched reports whether a form is one to alert on. No forms means all.
func (c cfg) watched(slug string) bool {
	if len(c.Forms) == 0 {
		return true
	}
	for _, f := range c.Forms {
		if strings.TrimSpace(f) == slug {
			return true
		}
	}
	return false
}

// includeValues defaults to true when the setting was never saved.
func (c cfg) includeValues() bool { return c.IncludeValues == nil || *c.IncludeValues }

// maxLen is the longest value sent; 500 when unset.
func (c cfg) maxLen() int {
	if c.MaxValueLength <= 0 {
		return 500
	}
	return c.MaxValueLength
}

// isSet reports whether the channel has everything it needs, whether or not
// it is switched on.
func (c cfg) isSet(channel string) bool {
	switch channel {
	case chanSlack:
		return strings.TrimSpace(c.SlackWebhookURL) != ""
	case chanTeams:
		return strings.TrimSpace(c.TeamsWorkflowURL) != ""
	case chanGChat:
		return strings.TrimSpace(c.GChatWebhookURL) != ""
	case chanTelegram:
		return strings.TrimSpace(c.TelegramBotToken) != "" && strings.TrimSpace(c.TelegramChatID) != ""
	}
	return false
}

// enabled reports whether the channel's switch is on.
func (c cfg) enabled(channel string) bool {
	switch channel {
	case chanSlack:
		return c.SlackEnabled
	case chanTeams:
		return c.TeamsEnabled
	case chanGChat:
		return c.GChatEnabled
	case chanTelegram:
		return c.TelegramEnabled
	}
	return false
}

// channels lists the channels that are switched on and set up, in a fixed
// order.
func (c cfg) channels() []string {
	var out []string
	for _, ch := range []string{chanSlack, chanTeams, chanGChat, chanTelegram} {
		if c.enabled(ch) && c.isSet(ch) {
			out = append(out, ch)
		}
	}
	return out
}
