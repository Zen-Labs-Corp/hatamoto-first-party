package main

// purgeRemoved deletes the rows of forms that are no longer in the setting, in
// one statement. The slug list is passed as one JSON array.
const purgeRemoved = `WITH a AS (
  DELETE FROM poll_response WHERE form NOT IN (SELECT jsonb_array_elements_text($1::jsonb)))
DELETE FROM poll_tally WHERE form NOT IN (SELECT jsonb_array_elements_text($1::jsonb))`

// purge removes the data of forms that left the setting. An empty setting
// removes everything.
func purge(cfgs []formCfg) error {
	slugs := make([]string, 0, len(cfgs))
	for _, c := range cfgs {
		slugs = append(slugs, c.Slug)
	}
	return dbExec(purgeRemoved, slugs)
}
