// Command form-alerts is a Hatamoto plugin. Posts each form submission to Slack, Microsoft Teams, Google Chat or Telegram the moment it arrives, with the fields you choose and a link to the submission in your admin.
package main

import "time"

// now is the clock; tests replace it.
var now = time.Now
