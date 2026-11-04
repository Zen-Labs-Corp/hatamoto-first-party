# polls

Turns Hatamoto forms into polls, surveys and scored quizzes, with live results, per-question breakdowns and CSV export. It does what plugins such as Crowdsignal/Polldaddy, UserFeedback and Quiz and Survey Master do on WordPress, using the forms you already have.

## Use

1. Install the plugin from Admin > Settings > Plugins.
2. Grant the permissions it asks for.
3. Open its **Settings** tab and add one entry under **Forms** for each form to count:
   - **Form slug**: the slug of an existing form.
   - **Kind**: poll, survey or quiz.
   - **Show results**: `always`, `after_close` or `never` (the default). Controls the public results page and the page builder source.
   - **Closes at**: answers after this time are not counted. A time without a zone is read as UTC. Leave it empty to count for ever.
   - **Answer key** (quiz): one row per question, with the field name and the correct value. For a checkbox question list several values separated by commas; the visitor must pick exactly those.
   - **Email the score** (quiz): sends the visitor their score once, to the first filled email field (or a field named `email`).
4. Open the plugin's page in the admin: it shows, for each form, the number of responses, a bar for every option of every question, the score distribution and average of a quiz, a **Recount** button and an **Export CSV** button. The CSV is shown in a text box to copy, because the admin page cannot download files.

The public results page is `/p/polls/results/<form slug>`. It has no script and shows counts only, never who answered. When results are not public, or the form is not set up, it answers 404 "Results are not available."

In the page builder, a loop bound to the source **Poll results** gets one item per option: title is the option label, category the question label, and the attributes `count`, `percent`, `form` and `field`. The source cannot pick a form per block: it uses the first form in the settings order whose results are public at that moment.

## Permissions

- `forms.read`: receive the `form.submitted` event, read the forms' fields and option labels, and read the stored submissions for a recount.
- `settings`: read the per-form configuration.
- `db`: keep the tallies and quiz scores in the plugin's own tables.
- `builder.data`: answer the "Poll results" page builder source.
- `mail.send`: email a quiz score to the visitor. Only used when **Email the score** is on for a form.
- `log`: record what a hook skipped, without addresses or answers.

The plugin makes no outbound requests and adds no script to your site.

## Data kept

- `poll_response`: one row per counted submission (form slug, submission id, and for a quiz the score and its maximum, the question options it chose, plus the time it was counted).
- `poll_tally`: one row per form, question and option with the number of times it was chosen.

No names, email addresses, free-text answers or IP addresses are stored. The rows of a form are kept until the form is removed from the setting; the next time the admin page loads or a Recount runs, they are deleted. Uninstalling the plugin keeps the data unless you choose to delete it.

## Build

From the repository root: `scripts/build-plugin.sh plugins/polls`.

## Limits

- Spam submissions are never counted. Core does not announce them, and a Recount skips them.
- Submissions after **Closes at** are not counted; core still accepts them.
- Only choice questions (select, radio, checkbox) are tallied. Other fields count toward the number of responses only. An answer that is not one of the question's current options is counted under "Other".
- The live count is not retried: if the plugin was down or timed out for a submission, that answer is missing until you press **Recount**, which rebuilds the form's numbers from the stored non-spam submissions. Answers submitted while a Recount runs are kept and counted.
- A Recount reads 200 submissions at a time and has about 8 seconds of the 10 second limit of a plugin route, so it handles a few thousand submissions. If a form is too large it stops with the message "Recount ran out of time" and the tallies stay as they were. It writes in parts that are each well under the 1 MiB request limit of the host, so size alone is not a limit.
- Quiz emails follow the host's mail limit (6 recipients a minute, with a burst of 10). Beyond it the email is dropped, not retried; the score is still stored.
- The page builder source cannot choose a form per block (see Use).
- A CSV export is limited by the 4 MiB response size of a plugin route.

## License

Apache-2.0.
