package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

var ErrNotLoggedIn = errors.New("not logged in; run `flomo login` first")

type Credentials struct {
	AccessToken string    `json:"access_token"`
	DeviceID    string    `json:"device_id"`
	Account     string    `json:"account,omitempty"`
	LoggedInAt  time.Time `json:"logged_in_at"`
}

type Store struct{ path string }

func NewStore(path string) *Store { return &Store{path: path} }

func (s *Store) Load() (Credentials, error) {
	if token := os.Getenv("FLOMO_TOKEN"); token != "" {
		return Credentials{AccessToken: token, DeviceID: envDeviceID()}, nil
	}
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return Credentials{}, ErrNotLoggedIn
	}
	if err != nil {
		return Credentials{}, fmt.Errorf("read credentials: %w", err)
	}
	var credentials Credentials
	if err := json.Unmarshal(b, &credentials); err != nil {
		return Credentials{}, fmt.Errorf("decode credentials: %w", err)
	}
	if credentials.AccessToken == "" {
		return Credentials{}, ErrNotLoggedIn
	}
	if credentials.DeviceID == "" {
		credentials.DeviceID = uuid.NewString()
	}
	return credentials, nil
}

func (s *Store) Save(credentials Credentials) error {
	if credentials.AccessToken == "" {
		return errors.New("empty access token")
	}
	if credentials.DeviceID == "" {
		credentials.DeviceID = uuid.NewString()
	}
	if credentials.LoggedInAt.IsZero() {
		credentials.LoggedInAt = time.Now()
	}
	b, err := json.MarshalIndent(credentials, "", "  ")
	if err != nil {
		return fmt.Errorf("encode credentials: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create credentials directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".auth-*")
	if err != nil {
		return fmt.Errorf("create temporary credentials: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("protect credentials: %w", err)
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return fmt.Errorf("write credentials: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close credentials: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("save credentials: %w", err)
	}
	return nil
}

func (s *Store) Delete() error {
	err := os.Remove(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete credentials: %w", err)
	}
	return nil
}

func envDeviceID() string {
	if id := os.Getenv("FLOMO_DEVICE_ID"); id != "" {
		return id
	}
	return "flomo-cli"
}
