package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/team-loco/loco/api/notify"
)

const (
	maxHookBody    = 1 << 20
	actionEmailOTP = "email"
)

type Hooks struct {
	webhook *WebhookVerifier
	policy  SignupPolicy
	mailer  notify.Mailer
	webURL  string
}

func NewHooks(webhook *WebhookVerifier, policy SignupPolicy, mailer notify.Mailer, webURL string) *Hooks {
	return &Hooks{webhook: webhook, policy: policy, mailer: mailer, webURL: strings.TrimSuffix(webURL, "/")}
}

func (h *Hooks) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /auth/hooks/before-user-created", h.beforeUserCreated)
	mux.HandleFunc("POST /auth/hooks/send-email", h.sendEmail)
}

type hookUser struct {
	Email       string         `json:"email"`
	NewEmail    string         `json:"new_email"`
	AppMetadata map[string]any `json:"app_metadata"`
}

type hookEmailData struct {
	Token           string `json:"token"`
	TokenHash       string `json:"token_hash"`
	RedirectTo      string `json:"redirect_to"`
	EmailActionType string `json:"email_action_type"`
	TokenNew        string `json:"token_new"`
	TokenHashNew    string `json:"token_hash_new"`
	OldEmail        string `json:"old_email"`
	Provider        string `json:"provider"`
	FactorType      string `json:"factor_type"`
}

type hookRequest struct {
	User      hookUser      `json:"user"`
	EmailData hookEmailData `json:"email_data"`
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

func (h *Hooks) sendEmail(w http.ResponseWriter, r *http.Request) {
	req, ok := h.read(w, r)
	if !ok {
		return
	}
	emails, err := h.authEmails(req)
	if err != nil {
		slog.ErrorContext(r.Context(), "cannot build auth email", "action", req.EmailData.EmailActionType, "error", err)
		hookError(w, http.StatusInternalServerError, "The email could not be sent.")
		return
	}
	for _, email := range emails {
		msg, renderErr := email.Render()
		if renderErr != nil {
			slog.ErrorContext(r.Context(), "cannot render auth email", "error", renderErr)
			hookError(w, http.StatusInternalServerError, "The email could not be sent.")
			return
		}
		if sendErr := h.mailer.Send(r.Context(), msg); sendErr != nil {
			slog.ErrorContext(r.Context(), "cannot send auth email",
				"action", req.EmailData.EmailActionType, "error", sendErr)
			hookError(w, http.StatusInternalServerError, "The email could not be sent. Try again in a minute.")
			return
		}
	}
	hookOK(w)
}

func (h *Hooks) confirmURL(tokenHash, verifyType, redirectTo string) string {
	q := url.Values{}
	q.Set("token_hash", tokenHash)
	q.Set("type", verifyType)
	if redirectTo != "" {
		q.Set("redirect_to", redirectTo)
	}
	return h.webURL + "/auth/confirm?" + q.Encode()
}

func (h *Hooks) authEmails(req hookRequest) ([]notify.Email, error) {
	d := req.EmailData
	to := req.User.Email
	switch d.EmailActionType {
	case "signup":
		return []notify.Email{{
			To:          to,
			Subject:     "Confirm your email for Loco",
			Heading:     "Confirm your email",
			Paragraphs:  []string{"Confirm this address to finish creating your Loco account."},
			ActionLabel: "Confirm email",
			ActionURL:   h.confirmURL(d.TokenHash, "signup", d.RedirectTo),
			Footnote:    "If you didn't sign up for Loco, you can ignore this email.",
		}}, nil
	case "magiclink":
		return []notify.Email{{
			To:          to,
			Subject:     "Your Loco sign-in link",
			Heading:     "Sign in to Loco",
			Paragraphs:  []string{"Use this link to sign in. It works once."},
			ActionLabel: "Sign in",
			ActionURL:   h.confirmURL(d.TokenHash, "magiclink", d.RedirectTo),
			Footnote:    "If you didn't ask to sign in, you can ignore this email.",
		}}, nil
	case "recovery":
		return []notify.Email{
			{
				To:      to,
				Subject: "Reset your Loco password",
				Heading: "Reset your password",
				Paragraphs: []string{
					"Someone asked to reset the password for this account. If it was you, choose a new one.",
				},
				ActionLabel: "Reset password",
				ActionURL:   h.confirmURL(d.TokenHash, "recovery", d.RedirectTo),
				Footnote:    "If you didn't ask for this, ignore this email. Your password stays the same.",
			},
		}, nil
	case "invite":
		return []notify.Email{{
			To:          to,
			Subject:     "You're invited to Loco",
			Heading:     "You're invited to Loco",
			Paragraphs:  []string{"Accept the invitation to create your account."},
			ActionLabel: "Accept invitation",
			ActionURL:   h.confirmURL(d.TokenHash, "invite", d.RedirectTo),
		}}, nil
	case actionEmailOTP:
		return []notify.Email{{
			To:         to,
			Subject:    "Your Loco sign-in code",
			Heading:    "Your sign-in code",
			Paragraphs: []string{"Enter this code to sign in to Loco."},
			Code:       d.Token,
			Footnote:   "If you didn't ask to sign in, you can ignore this email.",
		}}, nil
	case "reauthentication":
		return []notify.Email{{
			To:         to,
			Subject:    "Your Loco verification code",
			Heading:    "Confirm it's you",
			Paragraphs: []string{"Enter this code to continue."},
			Code:       d.Token,
		}}, nil
	case "email_change":
		return h.emailChange(req)
	default:
		if notice, ok := notification(d, to); ok {
			return []notify.Email{notice}, nil
		}
		return nil, fmt.Errorf("unknown email action %q", d.EmailActionType)
	}
}

func (h *Hooks) emailChange(req hookRequest) ([]notify.Email, error) {
	d := req.EmailData
	newEmail := req.User.NewEmail
	if newEmail == "" {
		return nil, errors.New("email change without a new address")
	}
	toNew := notify.Email{
		To:          newEmail,
		Subject:     "Confirm your new email for Loco",
		Heading:     "Confirm your new email",
		Paragraphs:  []string{fmt.Sprintf("Confirm %s as the new email address of your Loco account.", newEmail)},
		ActionLabel: "Confirm new email",
	}
	if d.TokenHashNew == "" {
		toNew.ActionURL = h.confirmURL(d.TokenHash, "email_change", d.RedirectTo)
		return []notify.Email{toNew}, nil
	}
	toNew.ActionURL = h.confirmURL(d.TokenHash, "email_change", d.RedirectTo)
	toCurrent := notify.Email{
		To:          req.User.Email,
		Subject:     "Confirm the change of your Loco email",
		Heading:     "Confirm your email change",
		Paragraphs:  []string{fmt.Sprintf("Someone asked to change this account's email to %s.", newEmail)},
		ActionLabel: "Confirm change",
		ActionURL:   h.confirmURL(d.TokenHashNew, "email_change", d.RedirectTo),
		Footnote:    "If this wasn't you, don't confirm, and change your password.",
	}
	return []notify.Email{toCurrent, toNew}, nil
}

func notificationHeading(actionType string) string {
	switch actionType {
	case "password_changed_notification":
		return "Your Loco password was changed"
	case "email_changed_notification":
		return "Your Loco email was changed"
	case "phone_changed_notification":
		return "Your Loco phone number was changed"
	case "identity_linked_notification":
		return "A sign-in method was added to your Loco account"
	case "identity_unlinked_notification":
		return "A sign-in method was removed from your Loco account"
	case "mfa_factor_enrolled_notification":
		return "Two-factor authentication was turned on"
	case "mfa_factor_unenrolled_notification":
		return "Two-factor authentication was turned off"
	default:
		return ""
	}
}

func notification(d hookEmailData, to string) (notify.Email, bool) {
	heading := notificationHeading(d.EmailActionType)
	if heading == "" {
		return notify.Email{}, false
	}
	return notify.Email{
		To:         to,
		Subject:    heading,
		Heading:    heading,
		Paragraphs: []string{"This is a record of a change to your account's security settings."},
		Footnote:   "If you didn't make this change, reset your password and contact support.",
	}, true
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
