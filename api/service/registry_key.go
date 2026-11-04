package service

import (
	"crypto/ed25519"
	"log/slog"
	"net/http"

	"github.com/team-loco/loco/api/pkg/registrytoken"
)

const registryKeyContentType = "application/x-pem-file"

func NewRegistryKeyHandler(key ed25519.PublicKey) (http.HandlerFunc, error) {
	body, err := registrytoken.EncodePublicKey(key)
	if err != nil {
		return nil, err
	}
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", registryKeyContentType)
		if _, err := w.Write(body); err != nil {
			slog.WarnContext(r.Context(), "write registry public key", "error", err)
		}
	}, nil
}
