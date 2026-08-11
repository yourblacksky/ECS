package session

import (
	"errors"
	"os"
	"testing"

	"ecs/internal/config"
	"ecs/internal/protocol"
)

func TestStorePersistsConversation(t *testing.T) {
	store, err := New(config.SessionsConfig{Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Append("local", "conversation/with unsafe filename",
		protocol.Message{Role: "user", Content: "你好"},
		protocol.Message{Role: "assistant", Content: "你好！"},
	)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load("local", "conversation/with unsafe filename")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Messages) != 2 || loaded.Messages[1].Content != "你好！" {
		t.Fatalf("unexpected conversation: %#v", loaded)
	}
}

func TestStoreIsolatesSameSessionIDByOwner(t *testing.T) {
	store, err := New(config.SessionsConfig{Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append("peer-a", "shared", protocol.Message{Role: "user", Content: "secret-a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load("peer-b", "shared"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("another principal loaded the session: %v", err)
	}
	if _, err := store.Append("peer-b", "shared", protocol.Message{Role: "user", Content: "secret-b"}); err != nil {
		t.Fatal(err)
	}
	a, err := store.Load("peer-a", "shared")
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.Load("peer-b", "shared")
	if err != nil {
		t.Fatal(err)
	}
	if a.Owner != "peer-a" || b.Owner != "peer-b" ||
		a.Messages[0].Content != "secret-a" || b.Messages[0].Content != "secret-b" {
		t.Fatalf("principal sessions were mixed: a=%#v b=%#v", a, b)
	}
}

func TestStoreOptionalMessageLimit(t *testing.T) {
	store, err := New(config.SessionsConfig{Directory: t.TempDir(), MaxMessages: 2})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Append("local", "limited",
		protocol.Message{Role: "user", Content: "one"},
		protocol.Message{Role: "assistant", Content: "two"},
		protocol.Message{Role: "user", Content: "three"},
	)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load("local", "limited")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Messages) != 2 || loaded.Messages[0].Content != "two" {
		t.Fatalf("unexpected limited conversation: %#v", loaded.Messages)
	}
}
