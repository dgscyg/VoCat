package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestAppendLogEventEnforcesHardLimit(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	if _, err := database.db.ExecContext(context.Background(), `
		WITH RECURSIVE sequence(value) AS (
			SELECT 1 UNION ALL SELECT value + 1 FROM sequence WHERE value <= ?
		)
		INSERT INTO log_events(event_time, level, message, caller, fields_json)
		SELECT value, 'info', 'seed-' || value, '', '{}' FROM sequence
	`, MaxLogEvents); err != nil {
		t.Fatal(err)
	}
	if _, err := database.AppendLogEvent(context.Background(), LogEvent{
		Level: "info", Message: "newest", Time: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	count, err := database.CountLogEvents(context.Background())
	if err != nil || count != MaxLogEvents {
		t.Fatalf("CountLogEvents = %d, %v; want %d", count, err, MaxLogEvents)
	}
	logs, err := database.ListLogEvents(context.Background(), LogFilter{Limit: 1})
	if err != nil || len(logs) != 1 || logs[0].Message != "newest" {
		t.Fatalf("newest log = %#v, %v", logs, err)
	}
}

func TestClearLogEventsRejectsAlreadyQueuedEntries(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	cutoff := time.Now().UTC()
	if _, err := database.AppendLogEvent(context.Background(), LogEvent{
		Level: "info", Message: "existing", Time: cutoff.Add(-time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	deleted, err := database.ClearLogEvents(context.Background(), cutoff)
	if err != nil || deleted != 1 {
		t.Fatalf("ClearLogEvents = %d, %v", deleted, err)
	}
	late, err := database.AppendLogEvent(context.Background(), LogEvent{
		Level: "info", Message: "queued-before-clear", Time: cutoff.Add(-time.Millisecond),
	})
	if err != nil || late.ID != 0 {
		t.Fatalf("old queued append = %+v, %v", late, err)
	}
	if _, err := database.AppendLogEvent(context.Background(), LogEvent{
		Level: "info", Message: fmt.Sprintf("new-%d", MaxLogEvents), Time: cutoff.Add(time.Millisecond),
	}); err != nil {
		t.Fatal(err)
	}
	count, err := database.CountLogEvents(context.Background())
	if err != nil || count != 1 {
		t.Fatalf("CountLogEvents = %d, %v; want 1", count, err)
	}
}
