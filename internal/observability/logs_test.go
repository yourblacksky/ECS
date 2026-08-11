package observability

import (
	"log/slog"
	"testing"
)

func TestLogStoreKeepsNewestAndFiltersLevels(t *testing.T) {
	store := NewLogStore(2)
	logger := slog.New(MultiHandler(store))
	logger.Info("one", "node", "a")
	logger.Warn("two")
	logger.Error("three")
	entries := store.Recent(10, slog.LevelInfo)
	if len(entries) != 2 || entries[0].Message != "three" || entries[1].Message != "two" {
		t.Fatalf("unexpected entries: %#v", entries)
	}
	errorsOnly := store.Recent(10, slog.LevelError)
	if len(errorsOnly) != 1 || errorsOnly[0].Level != "error" {
		t.Fatalf("level filter failed: %#v", errorsOnly)
	}
	slog.New(store.WithAttrs([]slog.Attr{slog.String("scope", "admin")})).Info("with attrs")
	withAttrs := store.Recent(1, slog.LevelInfo)
	if withAttrs[0].Attributes["scope"] != "admin" {
		t.Fatalf("bound attributes missing: %#v", withAttrs[0])
	}
}
