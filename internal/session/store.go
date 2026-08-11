package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"ecs/internal/config"
	"ecs/internal/protocol"
)

type Conversation struct {
	ID        string             `json:"id"`
	Owner     string             `json:"owner"`
	Messages  []protocol.Message `json:"messages"`
	CreatedAt time.Time          `json:"created_at"`
	UpdatedAt time.Time          `json:"updated_at"`
}

type Store struct {
	cfg config.SessionsConfig
	mu  sync.Mutex
}

func New(cfg config.SessionsConfig) (*Store, error) {
	if err := os.MkdirAll(cfg.Directory, 0o700); err != nil {
		return nil, fmt.Errorf("create session directory: %w", err)
	}
	return &Store{cfg: cfg}, nil
}

func (s *Store) Load(owner, id string) (Conversation, error) {
	if id == "" {
		return Conversation{}, errors.New("session id is required")
	}
	owner = normalizeOwner(owner)
	s.mu.Lock()
	defer s.mu.Unlock()
	conversation, err := s.loadLocked(owner, id)
	if errors.Is(err, os.ErrNotExist) && owner == "local" {
		conversation, err = s.loadLegacyLocked(id)
		if err == nil {
			conversation.Owner = owner
			if saveErr := s.saveLocked(conversation); saveErr != nil {
				return Conversation{}, saveErr
			}
		}
	}
	return conversation, err
}

func (s *Store) Append(owner, id string, messages ...protocol.Message) (Conversation, error) {
	if id == "" {
		return Conversation{}, errors.New("session id is required")
	}
	owner = normalizeOwner(owner)
	s.mu.Lock()
	defer s.mu.Unlock()
	conversation, err := s.loadLocked(owner, id)
	if errors.Is(err, os.ErrNotExist) && owner == "local" {
		conversation, err = s.loadLegacyLocked(id)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Conversation{}, err
	}
	now := time.Now().UTC()
	if errors.Is(err, os.ErrNotExist) {
		conversation = Conversation{ID: id, Owner: owner, CreatedAt: now}
	}
	conversation.Owner = owner
	conversation.Messages = append(conversation.Messages, messages...)
	if s.cfg.MaxMessages > 0 && len(conversation.Messages) > s.cfg.MaxMessages {
		conversation.Messages = append([]protocol.Message(nil), conversation.Messages[len(conversation.Messages)-s.cfg.MaxMessages:]...)
	}
	conversation.UpdatedAt = now
	if err := s.saveLocked(conversation); err != nil {
		return Conversation{}, err
	}
	return conversation, nil
}

func (s *Store) saveLocked(conversation Conversation) error {
	data, err := json.MarshalIndent(conversation, "", "  ")
	if err != nil {
		return err
	}
	path := s.path(conversation.Owner, conversation.ID)
	temp, err := os.CreateTemp(s.cfg.Directory, ".session-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempName, path); err != nil {
		return err
	}
	return nil
}

func (s *Store) loadLocked(owner, id string) (Conversation, error) {
	data, err := os.ReadFile(s.path(owner, id))
	if err != nil {
		return Conversation{}, err
	}
	var conversation Conversation
	if err := json.Unmarshal(data, &conversation); err != nil {
		return Conversation{}, err
	}
	if conversation.ID != id {
		return Conversation{}, errors.New("session id mismatch")
	}
	if normalizeOwner(conversation.Owner) != owner {
		return Conversation{}, errors.New("session owner mismatch")
	}
	return conversation, nil
}

func (s *Store) loadLegacyLocked(id string) (Conversation, error) {
	data, err := os.ReadFile(s.legacyPath(id))
	if err != nil {
		return Conversation{}, err
	}
	var conversation Conversation
	if err := json.Unmarshal(data, &conversation); err != nil {
		return Conversation{}, err
	}
	if conversation.ID != id || (conversation.Owner != "" && conversation.Owner != "local") {
		return Conversation{}, errors.New("legacy session identity mismatch")
	}
	return conversation, nil
}

func (s *Store) path(owner, id string) string {
	sum := sha256.Sum256([]byte(normalizeOwner(owner) + "\x00" + id))
	return filepath.Join(s.cfg.Directory, hex.EncodeToString(sum[:])+".json")
}

func (s *Store) legacyPath(id string) string {
	sum := sha256.Sum256([]byte(id))
	return filepath.Join(s.cfg.Directory, hex.EncodeToString(sum[:])+".json")
}

func normalizeOwner(owner string) string {
	if owner == "" {
		return "local"
	}
	return owner
}
