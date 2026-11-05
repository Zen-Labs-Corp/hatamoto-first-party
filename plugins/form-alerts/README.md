# form-alerts

Posts each form submission to Slack, Microsoft Teams, Google Chat or Telegram the moment it arrives, with the fields you choose and a link to the submission in your admin.

It replaces the WordPress add-ons that send form notifications to chat apps: the Slack and Telegram add-ons for Contact Form 7 and WPForms, Notification and Better Notifications.

## Use

1. Install the plugin from Admin > Settings > Plugins and grant the permissions it asks for.
2. Open its **Settings** tab, switch on a channel and paste its target. Any mix of channels can be on at once. How to get each target:
   - **Slack**: create an Incoming Webhook for your workspace and pick a channel. Paste the URL, which starts with `https://hooks.slack.com/services/`.
   - **Microsoft Teams**: Office 365 Connectors are retired, so use Workflows. In Teams (or Power Automate) create a flow from the template **Post to a channel when a webhook request is received**, pick the team and channel, then copy the HTTP POST URL the flow shows. Paste it in **Teams workflow URL**. Teams answers 202, which only means the flow accepted the request.
   - **Google Chat**: open the space, then the space menu > **Apps and integrations** > **Webhooks** > add a webhook, give it a name and copy its URL. It looks like `https://chat.googleapis.com/v1/spaces/.../messages?key=...&token=...`.
   - **Telegram**: create a bot with @BotFather and copy its token. Add the bot to the group, or make it an administrator of the channel. Paste the token and the chat: the numeric chat id (for example `-1001234567890`) or `@channelname`.
3. Optional: under **Forms to alert on** list form slugs, one per line. Empty means all forms.
4. Optional: under **Fields to include** list field keys, one per line. Empty means all fields except those whose key contains `password`, `token` or `card`.
5. **Include field values** turns the values on or off. **Longest value** (20 to 2000, default 500) cuts longer values and ends them with an ellipsis.
6. **Admin address** is your admin address, for example `https://example.com/admin`. Each message links to `<admin address>/settings/submissions/<id>`. Empty means the message shows the submission id and no link.
7. Open the plugin page. It shows each channel's state, a **Send test message** button per channel with the answer inline, a preview of the latest submission and the delivery log.

Each message is rendered when the form is submitted and sent within about a minute. The page only shows settings; change them in the Settings tab.

## Permissions

- `forms.read`: receive form submissions and list forms and the latest submission for the page.
- `settings`: read the channels, targets and options you saved.
- `http.fetch`: post to the chat platforms. Only these hosts are allowed: `hooks.slack.com`, `api.telegram.org`, `chat.googleapis.com`, `*.logic.azure.com`, `*.environment.api.powerplatform.com`.
- `db`: keep the delivery queue and the delivery log in the plugin's own tables.
- `log`: write host log lines about problems. They hold fixed words and counts, never field values, URLs or tokens.

## Data kept

- Queue: while a message is pending, its submission id, form slug, channel, the rendered message (which holds the chosen field values), the number of attempts and the time. A row is deleted when it is sent, when the platform rejects it, or 6 hours after the submission.
- Log: time, submission id, form slug, channel, status (sent, retrying, failed, expired) and the platform's HTTP code. Never field values, URLs or tokens. Rows older than 30 days are deleted.
- Webhook URLs and the bot token are stored in the settings, are never shown again, and never appear in the page, the log, the queue or any answer.

## Build

From the repository root: `scripts/build-plugin.sh plugins/form-alerts`.

## Limits

- Privacy: the field values you choose leave your site for Slack, Microsoft, Google or Telegram. Turn **Include field values** off to send only the form name and the admin link.
- Privacy: the link shows your admin address to the platform and to everyone in the channel. Leave **Admin address** empty to send only the submission id.
- Delivery takes about a minute, because a background job sends queued messages every minute.
- A 4xx answer from the platform is not retried and is logged as failed. A 429, 408 or 5xx answer, a timeout or a network error is retried with a growing wait, honouring Retry-After, for up to 6 hours, then logged as expired.
- Platforms limit how fast they accept messages (about one per second per channel); a burst is spread over retries.
- Each platform caps the message size. When a message is too large, the last fields are left out.
- Teams reports success once the flow accepts the request, not once the message is posted.
- Only form submissions, and only these four channels. No Discord, email or SMS, and no replies back to the form.

## License

Apache-2.0.
