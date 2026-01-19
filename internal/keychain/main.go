package keychain

import (
	"encoding/json"
	"time"

	"github.com/zalando/go-keyring"
)

const Service = "loco"

type UserToken struct {
	ExpiresAt time.Time
	Token     string
}

func SetLocoToken(user string, t UserToken) error {
	bytes, err := json.Marshal(t)
	if err != nil {
		return err
	}

	return keyring.Set(Service, user, string(bytes))
}

func GetLocoToken(user string) (*UserToken, error) {
	pass, err := keyring.Get(Service, user)
	if err != nil {
		return nil, err
	}
	t := new(UserToken)
	err = json.Unmarshal([]byte(pass), t)
	return t, err
}

func DeleteLocoToken(user string) error {
	return keyring.Delete(Service, user)
}
