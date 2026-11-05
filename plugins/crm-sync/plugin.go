// Command crm-sync is a Hatamoto plugin. Sends people who submit your forms to HubSpot, Pipedrive or Salesforce as contacts or leads, with field mapping, an optional consent field, a note with the message and retries.
//
// The form hook only validates a submission and queues one row: it never calls
// a provider. The deliver and retry crons send the queue, the refresh cron
// renews the Salesforce token. Problems end up in the delivery log, which holds
// no email and no field values.
package main

import (
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// now is the clock; tests replace it.
var now = time.Now

const (
	// maxAge is how long a submission may wait in the queue.
	maxAge = 24 * time.Hour
	// logKeep is how long delivery log rows are kept.
	logKeep = 30 * 24 * time.Hour
	// batchSize is how many rows one cron run claims.
	batchSize = 25
	// cronBudget and routeBudget are how long a cron or a route keeps starting
	// requests; the hook limit is 10 s (plugin.json).
	cronBudget  = 8 * time.Second
	routeBudget = 8 * time.Second
	// leaseTime is how long a claimed row is hidden from other runs.
	leaseTime = 2 * time.Minute
)

func init() {
	guest.OnAction(sdk.FormSubmitted, onFormSubmitted)
	guest.OnCron("deliver", onDeliver)
	guest.OnCron("retry", onRetry)
	guest.OnCron("refresh", onRefresh)
}
