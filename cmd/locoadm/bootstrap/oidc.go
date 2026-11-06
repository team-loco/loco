package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/team-loco/loco/cmd/internal/installation"
	"github.com/team-loco/loco/internal/bearer"
)

const (
	loginTimeout    = 5 * time.Minute
	headerTimeout   = 10 * time.Second
	shutdownTimeout = 2 * time.Second
	browserTimeout  = 5 * time.Second
)

var (
	errOIDC         = errors.New("identity provider sign-in failed")
	errIdentity     = errors.New("identity provider did not return the expected verified email")
	errOIDCCallback = fmt.Errorf("listen on configured bootstrap callback: %w", errOIDC)
)

func authenticate(
	ctx context.Context,
	config installation.Auth,
	expectedEmail string,
	output io.Writer,
	noBrowser bool,
) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, loginTimeout)
	defer cancel()
	provider, err := oidc.NewProvider(ctx, config.Issuer)
	if err != nil {
		return "", errOIDC
	}
	redirect, err := url.Parse(config.BootstrapRedirectURI)
	if err != nil {
		return "", errOIDC
	}
	var listenerConfig net.ListenConfig
	listener, err := listenerConfig.Listen(ctx, "tcp", redirect.Host)
	if err != nil {
		return "", errOIDCCallback
	}
	state, nonce, verifier := bearer.New(""), bearer.New(""), oauth2.GenerateVerifier()
	oauth := oauth2.Config{
		ClientID:    config.ClientID,
		RedirectURL: config.BootstrapRedirectURI,
		Endpoint:    provider.Endpoint(),
		Scopes:      []string{oidc.ScopeOpenID, "email", "profile"},
	}
	codes := make(chan string, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /callback", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != state {
			http.Error(w, "Invalid sign-in state", http.StatusBadRequest)
			return
		}
		if r.URL.Query().Get("error") != "" || r.URL.Query().Get("code") == "" {
			http.Error(w, "Sign-in failed", http.StatusBadRequest)
			select {
			case codes <- "":
			default:
			}
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if _, writeErr := io.WriteString(w, "Return to locoadm to complete administrator setup.\n"); writeErr != nil {
			return
		}
		select {
		case codes <- r.URL.Query().Get("code"):
		default:
		}
	})
	server := http.Server{Handler: mux, ReadHeaderTimeout: headerTimeout}
	done := make(chan struct{})
	go func() {
		defer close(done)
		serveErr := server.Serve(listener)
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			select {
			case codes <- "":
			default:
			}
		}
	}()
	defer func() {
		shutdownCtx, stop := context.WithTimeout(context.Background(), shutdownTimeout)
		defer stop()
		if shutdownErr := server.Shutdown(shutdownCtx); shutdownErr != nil {
			if closeErr := server.Close(); closeErr != nil {
				slog.DebugContext(ctx, "close bootstrap callback listener", "error", closeErr)
			}
		}
		<-done
	}()
	authorizationURL := oauth.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oidc.Nonce(nonce))
	if _, err = fmt.Fprintf(output, "Sign in as %s:\n%s\n", expectedEmail, authorizationURL); err != nil {
		return "", err
	}
	if !noBrowser {
		if err = openBrowser(ctx, authorizationURL); err != nil {
			if _, writeErr := fmt.Fprintln(output, "Open the sign-in URL in your browser."); writeErr != nil {
				return "", writeErr
			}
		}
	}
	var code string
	select {
	case <-ctx.Done():
		return "", errOIDC
	case code = <-codes:
	}
	if code == "" {
		return "", errOIDC
	}
	tokens, err := oauth.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return "", errOIDC
	}
	raw, ok := tokens.Extra("id_token").(string)
	if !ok {
		return "", errOIDC
	}
	identity, err := provider.Verifier(&oidc.Config{ClientID: config.ClientID}).Verify(ctx, raw)
	if err != nil || identity.Nonce != nonce {
		return "", errOIDC
	}
	var claims struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
	}
	if err = identity.Claims(&claims); err != nil {
		return "", errOIDC
	}
	if !claims.EmailVerified || claims.Email != expectedEmail {
		return "", errIdentity
	}
	if _, err = fmt.Fprintf(
		output,
		"Verified administrator: %s\nIssuer: %s\nSubject: %s\n",
		claims.Email,
		identity.Issuer,
		identity.Subject,
	); err != nil {
		return "", err
	}
	return raw, nil
}

func openBrowser(ctx context.Context, address string) error {
	ctx, cancel := context.WithTimeout(ctx, browserTimeout)
	defer cancel()
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.CommandContext(ctx, "open", address)
	case "linux":
		command = exec.CommandContext(ctx, "xdg-open", address)
	default:
		return errOIDC
	}
	return command.Run()
}
