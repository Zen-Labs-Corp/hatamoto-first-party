package main

import "hatamoto.dev/sdk"

// onEntityDeleted removes the votes and the stored path of a deleted entity.
// It fails open: a database error is not a hook failure.
func onEntityDeleted(ev sdk.EntityEvent) error {
	if !isUUID(ev.ID) {
		return nil
	}
	if err := dbExec(`DELETE FROM votes WHERE entity_id = $1::uuid`, ev.ID); err != nil {
		return nil
	}
	_ = dbExec(`DELETE FROM pages WHERE entity_id = $1::uuid`, ev.ID)
	return nil
}
