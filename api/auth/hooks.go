package auth

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
)

const maxHookBody = 1 << 20

type Hooks struct {
	webhook *WebhookVerifier
	policy  SignupPolicy
}

func NewHooks(webhook *WebhookVerifier, policy SignupPolicy) *Hooks {
	return &Hooks{webhook: webhook, policy: policy}
}

func (h *Hooks) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /auth/hooks/before-user-created", h.beforeUserCreated)
}

type hookUser struct {
	Email string `json:"email"`
}

type hookRequest struct {
	User hookUser `json:"user"`
}

func (h *Hooks) read(w http.ResponseWriter, r *http.Request) (hookRequest, bool) {
	body, err := h.webhook.Verify(r, maxHookBody)
	if err != nil {
		slog.WarnContext(r.Context(), "auth hook rejected", "path", r.URL.Path, "error", err)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return hookRequest{}, false
	}
	var req hookRequest
	if err := json.Unmarshal(body, &req); err != nil {
		slog.WarnContext(r.Context(), "auth hook payload is not valid JSON", "path", r.URL.Path, "error", err)
		hookError(w, http.StatusBadRequest, "The sign-in request could not be read.")
		return hookRequest{}, false
	}
	return req, true
}

func (h *Hooks) beforeUserCreated(w http.ResponseWriter, r *http.Request) {
	req, ok := h.read(w, r)
	if !ok {
		return
	}
	if err := h.policy.CheckEmail(req.User.Email); err != nil {
		if rejectedErr, ok := errors.AsType[*SignupRejectedError](err); ok {
			slog.InfoContext(r.Context(), "signup rejected by hook", "reason", rejectedErr.Message)
			hookError(w, http.StatusForbidden, rejectedErr.Message)
			return
		}
		slog.ErrorContext(r.Context(), "signup policy failed", "error", err)
		hookError(w, http.StatusInternalServerError, "Sign-up is unavailable right now.")
		return
	}
	hookOK(w)
}

func hookOK(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("{}")); err != nil {
		slog.Debug("write hook response", "error", err)
	}
}

func hookError(w http.ResponseWriter, code int, message string) {
	body, err := json.Marshal(map[string]any{"error": map[string]any{"http_code": code, "message": message}})
	if err != nil {
		http.Error(w, message, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(body); err != nil {
		slog.Debug("write hook response", "error", err)
	}
}
