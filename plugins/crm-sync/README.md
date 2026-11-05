# crm-sync

Sends people who submit your forms to HubSpot, Pipedrive or Salesforce as contacts or leads, with field mapping, an optional consent field, a note with the message and retries.

It replaces the WordPress HubSpot plugin, WP Fusion, and the Pipedrive and Salesforce add-ons for Contact Form 7, WPForms and Gravity Forms for the one job they share: a form submission becomes a contact or lead in your CRM.

## Use

1. Install the plugin from Admin > Settings > Plugins and grant the permissions it asks for.
2. Open its **Settings** tab, choose the provider and enter its credentials (see the setup steps below).
3. Under **Forms to sync** enter the form slugs, one per line. Empty means nothing is sent. Then set the email field (default `email`) and, if you want them, a consent field (a submission counts only when it is ticked, or `yes`, `true`, `1`, `on`, `checked`, `y`), whether to add a note, the lead source and an owner id.
4. Open the plugin page and press **Test connection**. Then set the field mapping: each row pairs a form field with a CRM field, chosen from dropdowns of the CRM's own fields. Until you save one, a default mapping is used (first name, last name, phone and company). Email always goes to the email field. Saving an empty table restores the default.

When a watched form is submitted, the form is never slowed: the plugin only queues the submission. Within about a minute the person is created in the CRM, or updated if the same email is already there, so nobody is duplicated. With **Add a note** on, a note holds the form name, the page, the time, the lead source and every submitted field. Failures that may pass are retried in the background. A submission without consent is logged as skipped and not sent. If the CRM refuses a mapped field, that field is dropped, named in the log message, and the rest is sent.

The plugin page shows the provider and forms, the field mapping and the last 100 deliveries.

### HubSpot

1. In HubSpot, create a private app (legacy private app) with the scopes `crm.objects.contacts.read`, `crm.objects.contacts.write` and `crm.schemas.contacts.read`. Notes are written with the contacts write scope.
2. Copy the access token into **Access token** in Settings. Set the lifecycle stage new contacts get (default `lead`).

### Pipedrive

1. In Pipedrive, open Personal preferences > API and copy your API token.
2. Enter it as **API token** and enter your company domain (`acme` or `acme.pipedrive.com`).
3. Leave **Create a lead** on to also create a lead for each person (an existing lead of that person is reused).

### Salesforce

1. In Salesforce Setup, create an External Client App (or a Connected App) with OAuth enabled and the client credentials flow enabled, and choose a run-as user that has API access to Leads, Contacts and Tasks.
2. Enter your My Domain (for example `acme.my.salesforce.com`, or for a sandbox `acme--dev.sandbox.my.salesforce.com`), the consumer key as **Client id** and the consumer secret as **Client secret**.
3. Choose whether people become Leads or Contacts. The default company is used when no company is mapped (a Lead needs one).

## Permissions

- `forms.read`: receive form submissions and list the fields of your forms for the mapping dropdowns.
- `settings`: read the provider, credentials, forms and options you saved.
- `http.fetch`: call the CRM's API. Only these hosts are allowed: `api.hubapi.com`, `*.pipedrive.com` (your company domain) and `*.my.salesforce.com` (your My Domain and instance).
- `db`: keep the delivery queue and the delivery log in the plugin's own tables.
- `kv`: keep the Salesforce access token and instance URL, the HubSpot portal id for record links and your saved field mapping.
- `log`: write host log lines about failures. They hold counts and fixed words, never email addresses, field values or secrets.

The manifest asks for SDK `^0.8`: 0.8 is the lowest SDK that has everything this plugin uses. The build pins v0.11.0.

## Data kept

- Queue: while a delivery is pending, the email address, the submission's field values (as text, long values cut) and the page path, until the delivery succeeds, fails for good, or is 24 hours old.
- Log: time, submission id, form slug, provider, status, record id and link, the HTTP code and a short message (CRM field names, never values). No email addresses and no field values. Rows older than 30 days are deleted.
- Credentials are stored in the settings (the secrets are write-only) and the Salesforce access token in the plugin's key-value store. They never appear in the page, the log or any response.

## Build

From the repository root: `scripts/build-plugin.sh plugins/crm-sync`.

## Limits

- One CRM at a time. Nothing is synced back from the CRM.
- No deals, pipelines or opportunities, and no attachments.
- HTTP 408, 429 and 5xx answers, timeouts and network errors are retried for up to 24 hours: after 5, 10, 20, 40 and 80 minutes, then every 2 hours. Any other 4xx answer is not retried and is logged as failed.
- A note that fails after the record was saved is logged on that row and not retried, so no record is duplicated.
- Pipedrive: when the person was saved but the lead step fails (for example the API user has no access to Leads), the delivery counts as created or updated, the note is still added to the person, the log message says `lead failed <code>`, and the lead is not retried.
- The lifecycle stage in HubSpot cannot move backwards; HubSpot refuses it and it is reported as a rejected field.
- The owner id applies to new records only; an existing record keeps its owner.
- HubSpot contacts are found and updated by email through HubSpot's upsert, as HubSpot documents it.
- Pipedrive: a mapped phone replaces the person's phone list. Pipedrive's search index can lag, so two submissions of one email within seconds may create two people.
- Salesforce: a Contact is created without an account.
- Delivery runs at most once a minute. A delivery that does not finish within one run's time budget is retried later (log message `too slow`).
- The field mapping has at most 50 rows and at most 50 forms can be synced.
- A submission with no valid email address (`name@example.com`, at most 254 characters) is skipped.

## License

Apache-2.0.
