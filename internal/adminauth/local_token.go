package adminauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ecs/internal/config"
)

const tokenBytes = 32

type LocalToken struct {
	Path        string
	Fingerprint string
	Generated   bool
}

// EnsureLocalToken supplies a stable, private Admin credential for the CLI's
// loopback-first development mode. Public listeners must provide an explicit
// credential before configuration validation and never reach this fallback.
func EnsureLocalToken(configPath string, cfg *config.Config) (LocalToken, error) {
	if cfg == nil || !cfg.Admin.Enabled || strings.TrimSpace(cfg.AdminToken) != "" {
		return LocalToken{}, nil
	}
	tokenPath := filepath.Join(filepath.Dir(configPath), "data", "admin.token")
	token, generated, err := loadOrCreate(tokenPath)
	if err != nil {
		return LocalToken{}, fmt.Errorf("initialize local Admin token: %w", err)
	}
	cfg.AdminToken = token
	return LocalToken{
		Path:        tokenPath,
		Fingerprint: Fingerprint(token),
		Generated:   generated,
	}, nil
}

func Fingerprint(token string) string {
	sum := sha256.Sum256([]byte(token))
	return "sha256:" + hex.EncodeToString(sum[:6])
}

func loadOrCreate(path string) (string, bool, error) {
	token, err := readPrivateToken(path)
	if err == nil {
		return token, false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", false, err
	}
	random := make([]byte, tokenBytes)
	if _, err := rand.Read(random); err != nil {
		return "", false, err
	}
	token = base64.RawURLEncoding.EncodeToString(random)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		token, readErr := readPrivateToken(path)
		return token, false, readErr
	}
	if err != nil {
		return "", false, err
	}
	if _, err = file.WriteString(token + "\n"); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		_ = os.Remove(path)
		return "", false, err
	}
	if closeErr != nil {
		return "", false, closeErr
	}
	return token, true, nil
}

func readPrivateToken(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", errors.New("Admin token path must be a regular file")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", errors.New("Admin token file is empty")
	}
	return token, nil
}
