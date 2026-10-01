package keychain

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/zalando/go-keyring"
)

const Service = "loco"

const StoreEnvVar = "LOCO__CREDENTIAL_STORE"

const credentialsFileName = "credentials.json"

var ErrNotFound = keyring.ErrNotFound

type UserToken struct {
	ExpiresAt    time.Time
	Token        string
	RefreshToken string
}

type TokenStore interface {
	Get() (*UserToken, error)
	Set(t UserToken) error
	Delete() error
}

func NewStore(user string) (TokenStore, error) {
	switch backend := os.Getenv(StoreEnvVar); backend {
	case "":
		return &KeyringStore{User: user}, nil
	case "keyring":
		return &KeyringStore{User: user}, nil
	case "file":
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get user home directory: %w", err)
		}
		return &FileStore{Path: filepath.Join(home, ".loco", credentialsFileName)}, nil
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
	if err := json.Unmarshal([]byte(pass), t); err != nil {
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
	if err := json.Unmarshal(bytes, t); err != nil {
		return nil, fmt.Errorf("failed to decode %s: %w", s.Path, err)
	}
	return t, nil
}

func (s *FileStore) Set(t UserToken) error {
	bytes, err := json.Marshal(t)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return fmt.Errorf("failed to create %s: %w", filepath.Dir(s.Path), err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.Path), credentialsFileName+".*")
	if err != nil {
		return fmt.Errorf("failed to create temp credentials file: %w", err)
	}
	defer os.Remove(tmp.Name())
	_, writeErr := tmp.Write(bytes)
	if err := errors.Join(writeErr, tmp.Close()); err != nil {
		return fmt.Errorf("failed to write credentials: %w", err)
	}
	if err := os.Rename(tmp.Name(), s.Path); err != nil {
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

func SetLocoToken(user string, t UserToken) error {
	s, err := NewStore(user)
	if err != nil {
		return err
	}
	return s.Set(t)
}

func GetLocoToken(user string) (*UserToken, error) {
	s, err := NewStore(user)
	if err != nil {
		return nil, err
	}
	return s.Get()
}

func DeleteLocoToken(user string) error {
	s, err := NewStore(user)
	if err != nil {
		return err
	}
	return s.Delete()
}
