package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"vocat/internal/loghub"
	"vocat/internal/store"
)

func TestHandleLogHistoryDeleteClearsMemoryAndDatabase(t *testing.T) {
	server := newSettingsTestServer(t)
	hub := loghub.New(slog.NewTextHandler(io.Discard, nil), 100)
	server.logs = hub
	server.logger = slog.New(hub)
	server.logger.Info("memory log")
	if _, err := server.store.AppendLogEvent(context.Background(), store.LogEvent{
		Level: "info", Message: "persisted log",
	}); err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodDelete, "/api/logs/history", nil)
	server.handleLogHistory(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if history := hub.History(10, slog.LevelDebug, ""); len(history) != 0 {
		t.Fatalf("memory history after clear = %#v", history)
	}
	count, err := server.store.CountLogEvents(context.Background())
	if err != nil || count != 0 {
		t.Fatalf("persisted count after clear = %d, %v", count, err)
	}
}
