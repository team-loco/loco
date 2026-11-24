package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/netip"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm"
	authv1 "github.com/team-loco/loco/gen/go/loco/auth/v1"
	"github.com/team-loco/loco/internal/bearer"
	"github.com/team-loco/loco/internal/rpcerr"
)

const (
	cliCodeTTL         = 5 * time.Minute
	deviceCodeTTL      = 10 * time.Minute
	devicePollInterval = 5 * time.Second
	userCodeAlphabet   = "BCDFGHJKLMNPQRSTVWXZ"
	userCodeLength     = 8
	forwardedForHeader = "X-Forwarded-For"
	maxStoredUserAgent = 256
)

var (
	ErrCLICodeInvalid    = errors.New("this sign-in code is invalid or has expired; run loco login again")
	ErrDeviceCodeExpired = errors.New("this device code has expired; run loco login --device again")
	ErrUserCodeInvalid   = errors.New("that code is invalid or has expired")
	ErrApproveFromWeb    = errors.New("approve CLI sign-ins from the Loco web app")
	ErrDevicePollTooFast = errors.New("slow down: poll less often")
	ErrRefreshInFlight   = errors.New(
		"another refresh of this session just finished; read the stored session and retry",
	)
	ErrAuthStoreAvailable = errors.New("sign-in is temporarily unavailable")
)

type AuthServer struct {
	db      *pgxpool.Pool
	queries genDb.Querier
	machine *tvm.VendingMachine
	state   authStore
	webURL  string
	web     WebAuth
	now     func() time.Time
}

func NewAuthServer(
	db *pgxpool.Pool,
	queries genDb.Querier,
	machine *tvm.VendingMachine,
	webURL string,
	web WebAuth,
) *AuthServer {
	return &AuthServer{
		db:      db,
		queries: queries,
		machine: machine,
		state:   pgAuthStore{queries: queries, maxEntries: maxAuthStates},
		webURL:  strings.TrimSuffix(webURL, "/"),
		web:     web,
		now:     time.Now,
	}
}

type cliGrant struct {
	UserID    uuid.UUID `json:"userId"`
	Challenge string    `json:"challenge,omitempty"`
}

type deviceGrant struct {
	UserAgent   string     `json:"userAgent"`
	IPAddress   string     `json:"ipAddress"`
	RequestedAt time.Time  `json:"requestedAt"`
	UserID      *uuid.UUID `json:"userId,omitempty"`
	ExpiresAt   time.Time  `json:"expiresAt"`
}

func deviceKey(deviceDigest string) string {
	return "cli:device:" + deviceDigest
}

func devicePollKey(deviceDigest string) string {
	return "cli:device-poll:" + deviceDigest
}

func userCodeKey(userCode string) string {
	return "cli:user-code:" + bearer.Hash(userCode)
}

// storedUserAgent keeps the first maxStoredUserAgent bytes of a device's User-Agent, as valid UTF-8.
func storedUserAgent(userAgent string) string {
	return strings.ToValidUTF8(userAgent[:min(len(userAgent), maxStoredUserAgent)], "")
}

func randomUserCode() (string, error) {
	out := make([]byte, userCodeLength)
	limit := big.NewInt(int64(len(userCodeAlphabet)))
	for i := range out {
		n, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return "", err
		}
		out[i] = userCodeAlphabet[n.Int64()]
	}
	return string(out), nil
}

func normalizeUserCode(code string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(code) {
		if strings.ContainsRune(userCodeAlphabet, r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func formatUserCode(code string) string {
	return code[:userCodeLength/2] + "-" + code[userCodeLength/2:]
}

func pkceMatches(verifier, challenge string) bool {
	sum := sha256.Sum256([]byte(verifier))
	computed := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(computed), []byte(challenge)) == 1
}

// clientAddress is where a request came from, recorded for display and audit only; never base an
// access decision on it. Each of trustedProxyHops proxies in front of the API appends the address
// it received the request from to X-Forwarded-For, so the entry that many places from the right is
// the first one a client cannot choose. Without trusted proxies, or when the header is shorter or
// unparsable, the peer address is used.
func clientAddress(info *connect.CallInfo, trustedProxyHops int) netip.Addr {
	if trustedProxyHops > 0 {
		var entries []string
		for _, line := range info.RequestHeader().Values(forwardedForHeader) {
			for entry := range strings.SplitSeq(line, ",") {
				entries = append(entries, strings.TrimSpace(entry))
			}
		}
		if len(entries) >= trustedProxyHops {
			if addr, err := netip.ParseAddr(entries[len(entries)-trustedProxyHops]); err == nil {
				return addr
			}
		}
	}
	peer, err := netip.ParseAddrPort(info.PeerAddr)
	if err != nil {
		return netip.Addr{}
	}
	return peer.Addr()
}

func webGrant(ctx context.Context) (cliGrant, error) {
	p, err := principalFrom(ctx)
	if err != nil {
		return cliGrant{}, err
	}
	entity := p.Entity
	if entity.Type != genDb.EntityTypeUser {
		return cliGrant{}, connect.NewError(connect.CodeUnauthenticated, publicUnauthorized)
	}
	if p.Method != genDb.DeploymentMethodWeb {
		return cliGrant{}, rpcerr.New(connect.CodePermissionDenied, ErrApproveFromWeb)
	}
	return cliGrant{UserID: entity.ID}, nil
}

func (s *AuthServer) issue(
	ctx context.Context,
	grant cliGrant,
	info *connect.CallInfo,
) (*authv1.CLITokens, error) {
	ip := clientAddress(info, s.web.TrustedProxyHops)
	ua := info.RequestHeader().Get("User-Agent")
	var access, refresh string
	err := withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if err := lockSessionAccount(ctx, qtx, grant.UserID); err != nil {
			return err
		}
		if _, err := qtx.GetIdentityForUser(ctx, grant.UserID); errors.Is(err, pgx.ErrNoRows) {
			return tvm.ErrInvalidExpiredToken
		} else if err != nil {
			return fmt.Errorf("get CLI identity: %w", err)
		}
		var issueErr error
		access, refresh, issueErr = s.machine.WithQueries(qtx).IssueSession(ctx, grant.UserID, ip, ua)
		if issueErr != nil {
			return issueErr
		}
		return events.Record(ctx, qtx, events.Event{
			Type:        events.CLILoginCompleted,
			ActorType:   string(genDb.EntityTypeUser),
			ActorID:     new(grant.UserID),
			SubjectType: events.SubjectUser,
			SubjectID:   new(grant.UserID),
			Data:        map[string]any{events.FieldIP: ip, events.FieldUserAgent: ua},
		})
	})
	if errors.Is(err, tvm.ErrInvalidExpiredToken) {
		return nil, connect.NewError(connect.CodeUnauthenticated, publicUnauthorized).WithCause(err)
	}
	if err != nil {
		return nil, fmt.Errorf("issue cli session: %w", err)
	}
	return &authv1.CLITokens{
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresIn:    int64(s.machine.Cfg.SessionAccessTokenDuration.Seconds()),
	}, nil
}

func (s *AuthServer) ApproveCLILogin(
	ctx context.Context,
	req *authv1.ApproveCLILoginRequest,
) (*authv1.ApproveCLILoginResponse, error) {
	grant, err := webGrant(ctx)
	if err != nil {
		return nil, err
	}
	grant.Challenge = req.GetCodeChallenge()
	code := bearer.New("")
	payload, err := json.Marshal(grant)
	if err != nil {
		return nil, fmt.Errorf("encode cli grant: %w", err)
	}
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		return events.Record(ctx, qtx, events.Event{
			Type:        events.CLILoginApproved,
			SubjectType: events.SubjectUser,
			SubjectID:   new(grant.UserID),
			Data:        map[string]any{events.FieldFlow: "loopback"},
		})
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to approve cli login", "error", err)
		return nil, authStoreUnavailable(err)
	}
	if setErr := s.state.Set(ctx, "cli:code:"+bearer.Hash(code), payload, cliCodeTTL); setErr != nil {
		return nil, authStoreUnavailable(setErr)
	}
	slog.InfoContext(ctx, "approved cli login", "userId", grant.UserID)
	return &authv1.ApproveCLILoginResponse{Code: code}, nil
}

func (s *AuthServer) take(ctx context.Context, key string, into any) (bool, error) {
	raw, err := s.state.Take(ctx, key)
	if errors.Is(err, errAuthStateNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(raw, into)
}

func (s *AuthServer) ExchangeCLICode(
	ctx context.Context,
	req *authv1.ExchangeCLICodeRequest,
) (*authv1.ExchangeCLICodeResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	var grant cliGrant
	found, err := s.take(ctx, "cli:code:"+bearer.Hash(req.GetCode()), &grant)
	if err != nil {
		slog.ErrorContext(ctx, "failed to read cli code", "error", err)
		return nil, authStoreUnavailable(err)
	}
	if !found || !pkceMatches(req.GetCodeVerifier(), grant.Challenge) {
		return nil, rpcerr.New(connect.CodeUnauthenticated, ErrCLICodeInvalid)
	}
	tokens, err := s.issue(ctx, grant, info)
	if err != nil {
		return nil, err
	}
	return &authv1.ExchangeCLICodeResponse{Tokens: tokens}, nil
}

func (s *AuthServer) StartDeviceLogin(
	ctx context.Context,
	_ *authv1.StartDeviceLoginRequest,
) (*authv1.StartDeviceLoginResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	deviceCode := bearer.New("")
	userCode, err := randomUserCode()
	if err != nil {
		return nil, fmt.Errorf("generate user code: %w", err)
	}
	requestedAt := s.now()
	grant := deviceGrant{
		UserAgent:   storedUserAgent(info.RequestHeader().Get("User-Agent")),
		RequestedAt: requestedAt,
		ExpiresAt:   requestedAt.Add(deviceCodeTTL),
	}
	if addr := clientAddress(info, s.web.TrustedProxyHops); addr.IsValid() {
		grant.IPAddress = addr.String()
	}
	payload, err := json.Marshal(grant)
	if err != nil {
		return nil, fmt.Errorf("encode device grant: %w", err)
	}
	deviceDigest := bearer.Hash(deviceCode)
	if err := s.state.Start(ctx, userCodeKey(userCode), []byte(deviceDigest), deviceCodeTTL); err != nil {
		slog.ErrorContext(ctx, "failed to store device user code", "error", err)
		return nil, authStoreUnavailable(err)
	}
	if err := s.state.Set(ctx, deviceKey(deviceDigest), payload, deviceCodeTTL); err != nil {
		slog.ErrorContext(ctx, "failed to store device code", "error", err)
		if _, takeErr := s.state.Take(ctx, userCodeKey(userCode)); takeErr != nil {
			slog.ErrorContext(ctx, "failed to release device user code", "error", takeErr)
		}
		return nil, authStoreUnavailable(err)
	}
	display := formatUserCode(userCode)
	return &authv1.StartDeviceLoginResponse{
		DeviceCode:              deviceCode,
		UserCode:                display,
		VerificationUri:         s.webURL + "/cli/device",
		VerificationUriComplete: s.webURL + "/cli/device?code=" + display,
		ExpiresIn:               int64(deviceCodeTTL.Seconds()),
		Interval:                int64(devicePollInterval.Seconds()),
	}, nil
}

func (s *AuthServer) GetDeviceLogin(
	ctx context.Context,
	req *authv1.GetDeviceLoginRequest,
) (*authv1.GetDeviceLoginResponse, error) {
	if _, err := webGrant(ctx); err != nil {
		return nil, err
	}
	userCode := normalizeUserCode(req.GetUserCode())
	if len(userCode) != userCodeLength {
		return nil, rpcerr.New(connect.CodeNotFound, ErrUserCodeInvalid)
	}
	deviceDigest, err := s.state.Get(ctx, userCodeKey(userCode))
	if errors.Is(err, errAuthStateNotFound) {
		return nil, rpcerr.New(connect.CodeNotFound, ErrUserCodeInvalid)
	}
	if err != nil {
		return nil, authStoreUnavailable(err)
	}
	raw, err := s.state.Get(ctx, deviceKey(string(deviceDigest)))
	if err != nil {
		return nil, rpcerr.New(connect.CodeNotFound, ErrUserCodeInvalid)
	}
	var device deviceGrant
	if decodeErr := json.Unmarshal(raw, &device); decodeErr != nil {
		return nil, fmt.Errorf("decode device grant: %w", decodeErr)
	}
	return &authv1.GetDeviceLoginResponse{
		UserAgent:   device.UserAgent,
		IpAddress:   device.IPAddress,
		RequestedAt: timestamppb.New(device.RequestedAt),
		ExpiresAt:   timestamppb.New(device.ExpiresAt),
	}, nil
}

func (s *AuthServer) ApproveDeviceLogin(
	ctx context.Context,
	req *authv1.ApproveDeviceLoginRequest,
) (*authv1.ApproveDeviceLoginResponse, error) {
	grant, err := webGrant(ctx)
	if err != nil {
		return nil, err
	}
	userCode := normalizeUserCode(req.GetUserCode())
	if len(userCode) != userCodeLength {
		return nil, rpcerr.New(connect.CodeNotFound, ErrUserCodeInvalid)
	}
	deviceDigest, err := s.state.Take(ctx, userCodeKey(userCode))
	if errors.Is(err, errAuthStateNotFound) {
		return nil, rpcerr.New(connect.CodeNotFound, ErrUserCodeInvalid)
	}
	if err != nil {
		return nil, authStoreUnavailable(err)
	}
	key := deviceKey(string(deviceDigest))
	raw, err := s.state.Get(ctx, key)
	if err != nil {
		return nil, rpcerr.New(connect.CodeNotFound, ErrUserCodeInvalid)
	}
	var device deviceGrant
	if decodeErr := json.Unmarshal(raw, &device); decodeErr != nil {
		return nil, fmt.Errorf("decode device grant: %w", decodeErr)
	}
	remaining := device.ExpiresAt.Sub(s.now())
	if remaining <= 0 {
		return nil, rpcerr.New(connect.CodeNotFound, ErrUserCodeInvalid)
	}
	device.UserID = &grant.UserID
	payload, err := json.Marshal(device)
	if err != nil {
		return nil, fmt.Errorf("encode device grant: %w", err)
	}
	// A retryable failure returns the user code to the store for the next approval attempt.
	unavailable := func(cause error) error {
		if restoreErr := s.state.Restore(ctx, userCodeKey(userCode), deviceDigest, remaining); restoreErr != nil {
			slog.ErrorContext(ctx, "failed to restore device user code", "error", restoreErr)
		}
		return authStoreUnavailable(cause)
	}
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		return events.Record(ctx, qtx, events.Event{
			Type:        events.CLILoginApproved,
			SubjectType: events.SubjectUser,
			SubjectID:   new(grant.UserID),
			Data:        map[string]any{events.FieldFlow: "device"},
		})
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to approve device login", "error", err)
		return nil, unavailable(err)
	}
	if setErr := s.state.Set(ctx, key, payload, remaining); setErr != nil {
		return nil, unavailable(setErr)
	}
	slog.InfoContext(ctx, "approved device login", "userId", grant.UserID)
	return &authv1.ApproveDeviceLoginResponse{}, nil
}

func (s *AuthServer) PollDeviceLogin(
	ctx context.Context,
	req *authv1.PollDeviceLoginRequest,
) (*authv1.PollDeviceLoginResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	deviceDigest := bearer.Hash(req.GetDeviceCode())
	key := deviceKey(deviceDigest)
	raw, err := s.state.Get(ctx, key)
	if errors.Is(err, errAuthStateNotFound) {
		return nil, rpcerr.New(connect.CodeNotFound, ErrDeviceCodeExpired)
	}
	if err != nil {
		return nil, authStoreUnavailable(err)
	}
	var device deviceGrant
	if decodeErr := json.Unmarshal(raw, &device); decodeErr != nil {
		return nil, fmt.Errorf("decode device grant: %w", decodeErr)
	}
	if device.UserID == nil {
		if throttleErr := s.throttleDevicePoll(ctx, deviceDigest); throttleErr != nil {
			return nil, throttleErr
		}
		return &authv1.PollDeviceLoginResponse{}, nil
	}
	_, err = s.state.Take(ctx, key)
	if errors.Is(err, errAuthStateNotFound) {
		return nil, rpcerr.New(connect.CodeNotFound, ErrDeviceCodeExpired)
	}
	if err != nil {
		return nil, authStoreUnavailable(err)
	}
	tokens, err := s.issue(ctx, cliGrant{UserID: *device.UserID}, info)
	if err != nil {
		return nil, err
	}
	return &authv1.PollDeviceLoginResponse{Tokens: tokens}, nil
}

func (s *AuthServer) throttleDevicePoll(ctx context.Context, deviceDigest string) error {
	pollKey := devicePollKey(deviceDigest)
	now := s.now()
	last, err := s.state.Get(ctx, pollKey)
	if err != nil && !errors.Is(err, errAuthStateNotFound) {
		return authStoreUnavailable(err)
	}
	if err == nil {
		var lastPoll time.Time
		if decodeErr := lastPoll.UnmarshalText(last); decodeErr != nil {
			return fmt.Errorf("decode last device poll: %w", decodeErr)
		}
		if now.Sub(lastPoll) < devicePollInterval-time.Second {
			return rpcerr.New(connect.CodeResourceExhausted, ErrDevicePollTooFast)
		}
	}
	stamp, err := now.MarshalText()
	if err != nil {
		return fmt.Errorf("encode device poll time: %w", err)
	}
	if setErr := s.state.Set(ctx, pollKey, stamp, devicePollInterval); setErr != nil {
		return authStoreUnavailable(setErr)
	}
	return nil
}

func (s *AuthServer) RefreshCLIToken(
	ctx context.Context,
	req *authv1.RefreshCLITokenRequest,
) (*authv1.RefreshCLITokenResponse, error) {
	access, newRefresh, err := s.refreshSession(ctx, req.GetRefreshToken(), genDb.SessionKindCli)
	if errors.Is(err, tvm.ErrRefreshRotated) {
		return nil, rpcerr.New(connect.CodeAborted, ErrRefreshInFlight)
	}
	if errors.Is(err, tvm.ErrInvalidExpiredToken) {
		return nil, connect.NewError(connect.CodeUnauthenticated, publicUnauthorized).WithCause(err)
	}
	if err != nil {
		return nil, fmt.Errorf("refresh session: %w", err)
	}
	return &authv1.RefreshCLITokenResponse{Tokens: &authv1.CLITokens{
		AccessToken:  access,
		RefreshToken: newRefresh,
		ExpiresIn:    int64(s.machine.Cfg.SessionAccessTokenDuration.Seconds()),
	}}, nil
}

func (s *AuthServer) refreshSession(
	ctx context.Context,
	token string,
	kind genDb.SessionKind,
) (string, string, error) {
	var access, refresh string
	var rejected bool
	var rotated bool
	txErr := withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		// Find the owner and lock the account before Refresh reads or mutates the session.
		session, err := qtx.GetSessionByRefreshToken(ctx, bearer.Hash(token))
		if errors.Is(err, pgx.ErrNoRows) {
			return tvm.ErrInvalidExpiredToken
		}
		if err != nil {
			return fmt.Errorf("get refresh owner: %w", err)
		}
		if session.Kind != kind {
			return tvm.ErrInvalidExpiredToken
		}
		if lockErr := lockSessionAccount(ctx, qtx, session.UserID); lockErr != nil {
			return lockErr
		}
		access, refresh, err = s.machine.WithQueries(qtx).Refresh(ctx, token, kind)
		if errors.Is(err, tvm.ErrRefreshRotated) {
			rotated = true
			return nil
		}
		if errors.Is(err, tvm.ErrInvalidExpiredToken) {
			// A replay rejects the request and commits session revocation.
			rejected = true
			return nil
		}
		return err
	})
	if txErr != nil {
		return "", "", txErr
	}
	if rejected {
		return "", "", tvm.ErrInvalidExpiredToken
	}
	if rotated {
		return "", "", tvm.ErrRefreshRotated
	}
	return access, refresh, nil
}

// lockSessionAccount fences cached approvals and refresh tokens against deletion and
// suspension without resolving an identity or provisioning a replacement user.
func lockSessionAccount(ctx context.Context, qtx *genDb.Queries, userID uuid.UUID) error {
	if err := authz.LockUsers(ctx, qtx, userID); errors.Is(err, pgx.ErrNoRows) {
		return tvm.ErrInvalidExpiredToken
	} else if err != nil {
		return fmt.Errorf("lock session account: %w", err)
	}
	user, err := qtx.GetUserByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("read session account: %w", err)
	}
	if user.Status != genDb.UserStatusActive {
		return tvm.ErrInvalidExpiredToken
	}
	return nil
}

// authStoreUnavailable reports a sign-in store failure the client can retry; err stays private.
func authStoreUnavailable(err error) *connect.Error {
	return connect.NewError(connect.CodeUnavailable, ErrAuthStoreAvailable.Error()).WithCause(err)
}
