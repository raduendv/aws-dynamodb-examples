package model

import "time"

// FormatTimestamp emits UTC timestamps in whole seconds or millisecond,
// microsecond, or nanosecond groups, matching the cross-SDK value contract.
// Physical index and ledger keys deliberately retain KeyTimestampLayout.
func FormatTimestamp(value time.Time) string {
	layout := time.RFC3339
	switch nanos := value.Nanosecond(); {
	case nanos == 0:
	case nanos%1_000_000 == 0:
		layout = "2006-01-02T15:04:05.000Z"
	case nanos%1_000 == 0:
		layout = "2006-01-02T15:04:05.000000Z"
	default:
		layout = KeyTimestampLayout
	}
	return value.UTC().Format(layout)
}
