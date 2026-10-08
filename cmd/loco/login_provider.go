package loco

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"
	"connectrpc.com/connect"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	authv1 "github.com/team-loco/loco/gen/go/loco/auth/v1"
	"github.com/team-loco/loco/gen/go/loco/auth/v1/authv1connect"
)

const loopbackTimeout = 5 * time.Minute

var errLoginTimedOut = errors.New("timed out waiting for the browser; run loco login again")

func randomURLToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

type loopbackResult struct {
	code string
	err  error
}

const loopbackPage = `<!doctype html><meta charset="utf-8"><title>Loco</title>` +
	`<body style="font-family:system-ui,sans-serif;padding:48px;text-align:center">` +
	`<h1 style="font-size:20px">%s</h1><p>%s</p></body>`

func writeLoopbackPage(w http.ResponseWriter, status int, title, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if _, err := fmt.Fprintf(w, loopbackPage, html.EscapeString(title), html.EscapeString(message)); err != nil {
		slog.Debug("write loopback page", "error", err)
	}
}

func browserLogin(
	ctx context.Context,
	authClient authv1connect.AuthServiceClient,
	webHost string,
	open func(context.Context, string) error,
) (*authv1.CLITokens, error) {
	state, err := randomURLToken()
	if err != nil {
		return nil, err
	}
	verifier, err := randomURLToken()
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen for the browser: %w", err)
	}
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		return nil, fmt.Errorf("%w: unexpected listener address", ErrCommandFailed)
	}

	results := make(chan loopbackResult, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("state") != state {
			writeLoopbackPage(
				w,
				http.StatusBadRequest,
				"Sign-in failed",
				"This link does not match the running loco login.",
			)
			return
		}
		if msg := q.Get("error"); msg != "" {
			writeLoopbackPage(w, http.StatusOK, "Sign-in canceled", "You can close this tab.")
			select {
			case results <- loopbackResult{err: fmt.Errorf("sign-in was canceled: %s", msg)}:
			default:
			}
			return
		}
		writeLoopbackPage(w, http.StatusOK, "You're signed in", "Return to your terminal. You can close this tab.")
		select {
		case results <- loopbackResult{code: q.Get("code")}:
		default:
		}
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if serveErr := srv.Serve(ln); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			slog.Debug("loopback server stopped", "error", serveErr)
		}
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if shutdownErr := srv.Shutdown(shutdownCtx); shutdownErr != nil {
			slog.Debug("loopback server shutdown", "error", shutdownErr)
		}
	}()

	q := url.Values{}
	q.Set("port", strconv.Itoa(addr.Port))
	q.Set("state", state)
	q.Set("challenge", challenge)
	loginURL := webHost + "/cli/login?" + q.Encode()
	fmt.Printf("Opening your browser to sign in. If it doesn't open, visit:\n\n  %s\n\n", loginURL)
	if openErr := open(ctx, loginURL); openErr != nil {
		slog.Debug("could not open browser", "error", openErr)
	}

	waitCtx, cancel := context.WithTimeout(ctx, loopbackTimeout)
	defer cancel()
	var result loopbackResult
	select {
	case <-waitCtx.Done():
		if errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
			return nil, errLoginTimedOut
		}
		return nil, waitCtx.Err()
	case result = <-results:
	}
	if result.err != nil {
		return nil, result.err
	}

	resp, err := authClient.ExchangeCLICode(ctx, connect.NewRequest(&authv1.ExchangeCLICodeRequest{
		Code:         result.code,
		CodeVerifier: verifier,
	}))
	if err != nil {
		cmdutil.LogRequestID(ctx, err, "failed to exchange cli code")
		return nil, err
	}
	return resp.Msg.GetTokens(), nil
}

func pollDeviceLogin(
	ctx context.Context,
	authClient authv1connect.AuthServiceClient,
	deviceCode string,
	interval time.Duration,
	slowDown time.Duration,
) (*authv1.CLITokens, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
		resp, err := authClient.PollDeviceLogin(ctx, connect.NewRequest(&authv1.PollDeviceLoginRequest{
			DeviceCode: deviceCode,
		}))
		if connect.CodeOf(err) == connect.CodeResourceExhausted {
			interval += slowDown
			continue
		}
		if err != nil {
			return nil, err
		}
		if tokens := resp.Msg.GetTokens(); tokens != nil {
			return tokens, nil
		}
	}
}

func deviceLogin(ctx context.Context, authClient authv1connect.AuthServiceClient) (*authv1.CLITokens, error) {
	started, err := authClient.StartDeviceLogin(ctx, connect.NewRequest(&authv1.StartDeviceLoginRequest{}))
	if err != nil {
		cmdutil.LogRequestID(ctx, err, "failed to start device login")
		return nil, err
	}
	msg := started.Msg
	pollCtx, cancelPoll := context.WithTimeout(ctx, time.Duration(msg.GetExpiresIn())*time.Second)
	defer cancelPoll()

	tokenChan := make(chan AuthTokenResponse, 1)
	errorChan := make(chan error, 1)
	var tokens *authv1.CLITokens
	go func() {
		got, pollErr := pollDeviceLogin(
			pollCtx,
			authClient,
			msg.GetDeviceCode(),
			time.Duration(msg.GetInterval())*time.Second,
			5*time.Second,
		)
		if pollErr != nil {
			errorChan <- pollErr
			return
		}
		tokens = got
		tokenChan <- AuthTokenResponse{AccessToken: got.GetAccessToken()}
	}()

	fm, err := tea.NewProgram(initialModel(msg.GetUserCode(), msg.GetVerificationUri(), tokenChan, errorChan)).Run()
	if err != nil {
		return nil, err
	}
	finalM, ok := fm.(model)
	if !ok {
		return nil, fmt.Errorf("%w: unexpected model type", ErrCommandFailed)
	}
	if finalM.err != nil {
		return nil, finalM.err
	}
	if finalM.tokenResp == nil || tokens == nil {
		return nil, errors.New("login canceled")
	}
	return tokens, nil
}
