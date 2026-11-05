# auto-translate

Machine translation with DeepL or Google Cloud Translation: stages translated copies of your pages and posts for review, linked as translations.

It replaces WordPress plugins such as GTranslate, TranslatePress and Weglot. It translates ahead of time and stages the copies for your review; nothing is translated live when a page is viewed.

## Use

1. Install the plugin from Admin > Settings > Plugins.
2. Grant the permissions it asks for.
3. Open its **Settings** tab and choose the provider (DeepL or Google Cloud Translation), paste the API key, and optionally add glossary terms and, for DeepL, a formality.
4. Open **Runs > Machine translation > Start**. Enter the target locale (a locale the site serves, for example `de`), choose the content type (page or post) and, if you like, tick "only entries without a translation in this locale".
5. In the start dialog, tick the provider host. The run can only call hosts you confirmed there.
6. When the run is done, review the staged copies in the changeset and publish them. Each copy is linked to its source as a translation.

A run translates published, public entries of one content type into one locale. Running it again updates the copies it staged before instead of adding new ones, and a copy that is already published keeps its address.

## Permissions

- `entities.read`: reads the published pages and posts to translate, and which of them already have a translation.
- `settings`: stores the provider, the API key, the glossary and the formality.
- `http.fetch`: calls DeepL or Google Cloud Translation. The only hosts are `api.deepl.com`, `api-free.deepl.com` (keys ending in `:fx`) and `translation.googleapis.com`.

## Data kept

The plugin keeps nothing outside the run's temporary buffer, which is discarded with the run. The text of your pages is sent to the provider you chose; see that provider's terms. The API key is stored write-only in the plugin settings and is sent only in a request header, never in a URL, a log line or an error message.

## Build

From the repository root: `scripts/build-plugin.sh plugins/auto-translate`.

## Limits

- Entries with page builder content are skipped and counted in the run summary, even when they also have a plain body. Only entries with a plain body are translated.
- Only the title, slug, body, excerpt, description, SEO title and description, and social title and description are translated. The canonical URL is not copied, so a copy is its own canonical page. Other fields are copied unchanged.
- The plugin makes one provider call per step, so a large site takes many steps.
- The run buffer holds at most 10,000 keys and 64 MiB. Every translated entry uses one key, and every fetch step adds one more for its statistics, so a run reaches the key limit somewhat before 10,000 entries. Entries of 100 KiB each fill the buffer at about 650 entries, and the run then fails.
- Entries in other locales than the site language are read and sent to the provider, which bills for them, and are dropped when the copies are staged (counted as other language in the run summary).
- A list page the host finds too large is read again in smaller pieces. If even one entry cannot be read, the run fails.
- A re-run keeps the address of a copy that is already published. Copies that were staged but are not published yet are not seen by "only missing", so they are translated again.
- An entry too large for one provider request is skipped and counted as too large.
- The locale must be one the site serves. That is checked only when the copies are staged, after the provider has been called; a locale the provider does not support is refused before any call.
- The glossary keeps a term as written or replaces it with a fixed translation. Terms are matched in text only (not in links, attributes, scripts or styles), ignoring case and only as whole words. At most 100 entries. A term the provider alters anyway is counted in the run summary.
- The provider hosts must be ticked when the run starts.
- Formality applies to DeepL only, and only for target languages that support it.
- Machine output should be reviewed before it is published.

## License

Apache-2.0.
