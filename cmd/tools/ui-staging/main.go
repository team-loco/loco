// ui-staging runs the dashboard against staging using the existing CLI login.
package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/team-loco/loco/cmd/internal/auth"
	"github.com/team-loco/loco/cmd/internal/keychain"
)

const stagingAPI = "https://api.staging.loco.build"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil && ctx.Err() == nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	store, err := keychain.ForCurrentUser()
	if err != nil {
		return err
	}
	if _, err = auth.FreshToken(ctx, stagingAPI, store); err != nil {
		return fmt.Errorf(
			"staging login required: run loco login --host %s --web-host https://staging.loco.build: %w",
			stagingAPI,
			err,
		)
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	bridgeKey := rand.Text()
	upstream, err := url.Parse(stagingAPI)
	if err != nil {
		return err
	}
	server := &http.Server{Handler: stagingProxy(upstream, store, bridgeKey), ReadHeaderTimeout: 10 * time.Second}
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- server.Serve(listener) }()
	defer func() {
		if err := server.Close(); err != nil {
			log.Print(err)
		}
		if err := <-serverErrors; !errors.Is(err, http.ErrServerClosed) {
			log.Print(err)
		}
	}()
	cmd := exec.CommandContext(ctx, "bun", "run", "dev")
	cmd.Dir = "../web"
	cmd.Env = append(
		os.Environ(),
		"PUBLIC_API_URL=/api",
		"LOCO_UI_PROXY_URL=http://"+listener.Addr().String(),
		"LOCO_UI_PROXY_KEY="+bridgeKey,
	)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	fmt.Println(
		"Local UI uses staging data. Actions change staging. Browser sign-in and sign-out are not supported in this mode.",
	)
	return cmd.Run()
}

func stagingProxy(upstream *url.URL, store keychain.TokenStore, bridgeKey string) http.Handler {
	var mu sync.Mutex
	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(upstream)
			r.Out.Header.Del("Origin")
			r.Out.Header.Del("Cookie")
			r.Out.Header.Del("Referer")
		},
		FlushInterval: -1,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+bridgeKey {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		// Serialize refreshes: the CLI refresh token rotates after use.
		mu.Lock()
		token, err := auth.FreshToken(r.Context(), upstream.String(), store)
		mu.Unlock()
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(
				w,
				`{"code":"unauthenticated","message":"Staging login expired. Run loco login `+
					`--host https://api.staging.loco.build --web-host https://staging.loco.build `+
					`and restart mise run ui:staging."}`,
			)
			return
		}
		r.Header.Set("Authorization", "Bearer "+token.Token)
		proxy.ServeHTTP(w, r)
	})
}
