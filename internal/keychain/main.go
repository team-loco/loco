package keychain

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"time"

	"github.com/team-loco/loco/internal/session"
	"github.com/zalando/go-keyring"
)

const Service = "loco"

const StoreEnvVar = "LOCO_CREDENTIAL_STORE"

const credentialsFileName = "credentials.json"

var ErrNotFound = keyring.ErrNotFound

type UserToken struct {
	Host         string
	ExpiresAt    time.Time
	Token        string
	RefreshToken string
}

type TokenStore interface {
	Get() (*UserToken, error)
	Set(t UserToken) error
	Delete() error
}

func ForCurrentUser() (TokenStore, error) {
	currentUser, err := user.Current()
	if err != nil {
		return nil, fmt.Errorf("failed to get current user: %w", err)
	}
	return NewStore(currentUser)
}

func NewStore(u *user.User) (TokenStore, error) {
	keyringStore := &KeyringStore{User: u.Username}
	switch backend := os.Getenv(StoreEnvVar); backend {
	case "":
		return keyringStore, nil
	case "keyring":
		return keyringStore, nil
	case "file":
		locoDir, err := session.Dir()
		if err != nil {
			return nil, err
		}
		credentialsPath := filepath.Join(locoDir, credentialsFileName)
		return &FileStore{Path: credentialsPath}, nil
	default:
		return nil, fmt.Errorf("unknown %s %q: expected \"keyring\" or \"file\"", StoreEnvVar, backend)
	}
}

type KeyringStore struct {
	User string
}

func (s *KeyringStore) Get() (*UserToken, error) {
	pass, err := keyring.Get(Service, s.User)
	if err != nil {
		return nil, err
	}
	t := new(UserToken)
	if err = json.Unmarshal([]byte(pass), t); err != nil {
		return nil, fmt.Errorf("failed to decode token: %w", err)
	}
	return t, nil
}

func (s *KeyringStore) Set(t UserToken) error {
	bytes, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return keyring.Set(Service, s.User, string(bytes))
}

func (s *KeyringStore) Delete() error {
	return keyring.Delete(Service, s.User)
}

type FileStore struct {
	Path string
}

func (s *FileStore) Get() (*UserToken, error) {
	bytes, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", s.Path, err)
	}
	t := new(UserToken)
	if err = json.Unmarshal(bytes, t); err != nil {
		return nil, fmt.Errorf("failed to decode %s: %w", s.Path, err)
	}
	return t, nil
}

func (s *FileStore) Set(t UserToken) error {
	bytes, err := json.Marshal(t)
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.Path)
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("failed to create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, credentialsFileName+".*")
	if err != nil {
		return fmt.Errorf("failed to create temp credentials file: %w", err)
	}
	defer os.Remove(tmp.Name())
	_, writeErr := tmp.Write(bytes)
	if err = errors.Join(writeErr, tmp.Close()); err != nil {
		return fmt.Errorf("failed to write credentials: %w", err)
	}
	if err = os.Rename(tmp.Name(), s.Path); err != nil {
		return fmt.Errorf("failed to write credentials: %w", err)
	}
	return nil
}

func (s *FileStore) Delete() error {
	err := os.Remove(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	return err
}
