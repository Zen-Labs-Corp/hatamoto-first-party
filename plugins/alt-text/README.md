# alt-text

Suggests alt text for images in your media library that have none, using a vision model you choose, and stages each one for your review.

It does the job of the "auto image attributes" and AI alt-text plugins for WordPress: a vision model describes each image, the description is staged as a change, and nothing on your site changes until you publish it.

## Use

1. Install the plugin from Admin > Settings > Plugins and grant the permissions it asks for.
2. Open its **Settings** tab and fill in:
   - **Vision provider**: OpenAI, Anthropic or Gemini.
   - **API key**: the key of that provider. It is stored in the plugin settings and never shown again.
   - **Model**: optional, any model of the provider that reads images. Empty uses `gpt-4o-mini`, `claude-haiku-4-5` or `gemini-2.0-flash`.
   - **Language of the alt text**: a language name or code, default `en`.
   - **Longest alt text**: default 125 characters, between 20 and 300.
   - **Address of this site**: for example `https://example.com`. Media links are relative (`/media/...`) unless your install serves media from an absolute address, and a provider can only open a full link, so set this unless your media links are already absolute. Without it the run stops and says so.
   - **Images to check per run**: from 1 to 200, default 50.
   - **Only images without alt text**: on by default. Turn it off to also get suggestions for images that already have alt text.
3. Open the plugin's runs and choose **Suggest alt text**. The run has no form fields of its own; the two settings above decide how many images it checks and which ones. Save the settings first, then start it.
4. When the run starts, tick the host of your provider: `api.openai.com`, `api.anthropic.com` or `generativelanguage.googleapis.com`. Only the OpenAI host is ticked for you; if yours is not ticked the run stops and names it. Then wait. The run page shows how far it is and, at the end, the counts: images checked, suggestions, decorative images skipped, errors.
5. Review the staged changes and publish or discard them. Each suggestion is plain text, at most the length you set, without a lead such as "image of".

Images the model judges purely decorative (a divider, a background pattern) are not given alt text. Their file names are kept in the summary stored with the run, not on the run page.

An image the provider cannot describe (an error, an answer that was cut off, an unreachable link) is skipped and counted as an error; the run goes on with the next one.

## Permissions

- `settings`: reads the provider, key, model, language, length and site address you set.
- `media.read`: lists the images and reads each one's file type, public link and current alt text.
- `http.fetch`: sends each image's public link to the provider you chose. Allowed hosts: `api.openai.com`, `api.anthropic.com` and `generativelanguage.googleapis.com`. You confirm the hosts each time you start a run.

## Data kept

The plugin has no database and keeps nothing outside a run. Suggestions wait in the run's buffer and changeset until you publish or discard them. The API key stays in the plugin settings and is never written to a log, a staged change or a run message.

Each image's public link and the instructions for the model go to the provider you chose, under that provider's terms. The plugin does not send your pages, text or any other data.

## Build

From the repository root: `scripts/build-plugin.sh plugins/alt-text`. It builds a `wasip1` c-shared module, as every plugin here.

## Limits

- Only images that a public page uses are reachable (the rule for media reads while a run fetches). Library files no page uses are not checked.
- A provider must be able to open the image link, so images behind a login or on a private network fail. Gemini may refuse some links; those images are counted as errors.
- One vision call per step and at most 200 images per run.
- Only JPEG, PNG, GIF and WebP are sent. SVG and other formats are skipped.
- No captions, and no renaming of files.

## License

Apache-2.0.
