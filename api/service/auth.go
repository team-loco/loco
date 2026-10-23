package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"math/big"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/contextkeys"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/cache"
	"github.com/team-loco/loco/api/tvm"
	authv1 "github.com/team-loco/loco/gen/go/loco/auth/v1"
	"github.com/team-loco/loco/internal/bearer"
	"github.com/team-loco/loco/internal/httputil"
	"github.com/team-loco/loco/internal/rpcerr"
)

const (
	cliCodeTTL         = 5 * time.Minute
	deviceCodeTTL      = 10 * time.Minute
	devicePollInterval = 5 * time.Second
	userCodeAlphabet   = "BCDFGHJKLMNPQRSTVWXZ"
	userCodeLength     = 8
)

var (
	ErrCLICodeInvalid     = errors.New("this sign-in code is invalid or has expired; run loco login again")
	ErrDeviceCodeExpired  = errors.New("this device code has expired; run loco login --device again")
	ErrUserCodeInvalid    = errors.New("that code is invalid or has expired")
	ErrApproveFromWeb     = errors.New("approve CLI sign-ins from the Loco web app")
	ErrSignInRevoked      = errors.New("your sign-in is no longer valid; run loco login")
	ErrDevicePollTooFast  = errors.New("slow down: poll less often")
	ErrAuthStoreAvailable = errors.New("sign-in is temporarily unavailable")
)

type AuthServer struct {
	db      *pgxpool.Pool
	queries genDb.Querier
	machine *tvm.VendingMachine
	cache   cache.Cache
	admins  auth.Admins
	webURL  string
	now     func() time.Time
}

func NewAuthServer(
	db *pgxpool.Pool,
	queries genDb.Querier,
	machine *tvm.VendingMachine,
	store cache.Cache,
	admins auth.Admins,
	webURL string,
) *AuthServer {
	return &AuthServer{
		db:      db,
		queries: queries,
		machine: machine,
		cache:   store,
		admins:  admins,
		webURL:  strings.TrimSuffix(webURL, "/"),
		now:     time.Now,
	}
}

type cliGrant struct {
	UserID     uuid.UUID `json:"userId"`
	IdentityID uuid.UUID `json:"identityId"`
	Challenge  string    `json:"challenge,omitempty"`
}

type deviceGrant struct {
	UserCode   string     `json:"userCode"`
	UserID     *uuid.UUID `json:"userId,omitempty"`
	IdentityID *uuid.UUID `json:"identityId,omitempty"`
	ExpiresAt  time.Time  `json:"expiresAt"`
}

func deviceKey(deviceDigest string) string {
	return "cli:device:" + deviceDigest
}

func devicePollKey(deviceDigest string) string {
	return "cli:device-poll:" + deviceDigest
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
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

func clientInfo(h httputil.HeaderGetter) (string, string) {
	ip := h.Get("X-Real-IP")
	if ip == "" {
		ip = h.Get("X-Forwarded-For")
	}
	return ip, h.Get("User-Agent")
}

func (s *AuthServer) webIdentity(ctx context.Context) (cliGrant, error) {
	entity, ok := ctx.Value(contextkeys.EntityKey).(genDb.Entity)
	if !ok || entity.Type != genDb.EntityTypeUser {
		return cliGrant{}, rpcerr.New(connect.CodeUnauthenticated, ErrUnauthorized)
	}
	identity, ok := ctx.Value(contextkeys.IdentityKey).(auth.Identity)
	if !ok {
		return cliGrant{}, rpcerr.New(connect.CodePermissionDenied, ErrApproveFromWeb)
	}
	row, err := s.queries.GetIdentity(ctx, genDb.GetIdentityParams{Issuer: identity.Issuer, Subject: identity.Subject})
	if err != nil {
		slog.ErrorContext(ctx, "failed to look up identity for cli approval", "error", err)
		return cliGrant{}, rpcerr.New(connect.CodeInternal, ErrDB)
	}
	if row.UserID != entity.ID {
		return cliGrant{}, rpcerr.New(connect.CodePermissionDenied, ErrUnauthorized)
	}
	return cliGrant{UserID: entity.ID, IdentityID: row.ID}, nil
}

func (s *AuthServer) issue(
	ctx context.Context,
	grant cliGrant,
	h httputil.HeaderGetter,
) (*authv1.CLITokens, error) {
	ip, ua := clientInfo(h)
	identityID := grant.IdentityID
	var access, refresh string
	err := withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		var issueErr error
		access, refresh, issueErr = s.machine.WithQueries(qtx).IssueSession(ctx, grant.UserID, &identityID, ip, ua)
		if issueErr != nil {
			return issueErr
		}
		return events.Record(ctx, qtx, events.Event{
			Type:        events.CLILoginCompleted,
			ActorType:   string(genDb.EntityTypeUser),
			ActorID:     new(grant.UserID),
			SubjectType: events.SubjectUser,
			SubjectID:   new(grant.UserID),
			Data:        map[string]any{"ip": ip, "userAgent": ua},
		})
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to issue cli session", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrAuthStoreAvailable)
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
	grant, err := s.webIdentity(ctx)
	if err != nil {
		return nil, err
	}
	grant.Challenge = req.GetCodeChallenge()
	code := bearer.New("")
	payload, err := json.Marshal(grant)
	if err != nil {
		return nil, rpcerr.New(connect.CodeInternal, ErrAuthStoreAvailable)
	}
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if recordErr := events.Record(ctx, qtx, events.Event{
			Type:        events.CLILoginApproved,
			SubjectType: events.SubjectUser,
			SubjectID:   new(grant.UserID),
			Data:        map[string]any{"flow": "loopback"},
		}); recordErr != nil {
			return recordErr
		}
		return s.cache.Set(ctx, "cli:code:"+digest(code), payload, cliCodeTTL)
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to approve cli login", "error", err)
		return nil, rpcerr.New(connect.CodeUnavailable, ErrAuthStoreAvailable)
	}
	slog.InfoContext(ctx, "approved cli login", "userId", grant.UserID)
	return &authv1.ApproveCLILoginResponse{Code: code}, nil
}

func (s *AuthServer) take(ctx context.Context, key string, into any) (bool, error) {
	raw, err := s.cache.Get(ctx, key)
	if errors.Is(err, cache.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	took, err := s.cache.Take(ctx, key)
	if err != nil || !took {
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
	found, err := s.take(ctx, "cli:code:"+digest(req.GetCode()), &grant)
	if err != nil {
		slog.ErrorContext(ctx, "failed to read cli code", "error", err)
		return nil, rpcerr.New(connect.CodeUnavailable, ErrAuthStoreAvailable)
	}
	if !found || !pkceMatches(req.GetCodeVerifier(), grant.Challenge) {
		return nil, rpcerr.New(connect.CodeUnauthenticated, ErrCLICodeInvalid)
	}
	tokens, err := s.issue(ctx, grant, info.RequestHeader())
	if err != nil {
		return nil, err
	}
	return &authv1.ExchangeCLICodeResponse{Tokens: tokens}, nil
}

func (s *AuthServer) StartDeviceLogin(
	ctx context.Context,
	_ *authv1.StartDeviceLoginRequest,
) (*authv1.StartDeviceLoginResponse, error) {
	deviceCode := bearer.New("")
	userCode, err := randomUserCode()
	if err != nil {
		return nil, rpcerr.New(connect.CodeInternal, ErrAuthStoreAvailable)
	}
	expiresAt := s.now().Add(deviceCodeTTL)
	payload, err := json.Marshal(deviceGrant{UserCode: userCode, ExpiresAt: expiresAt})
	if err != nil {
		return nil, rpcerr.New(connect.CodeInternal, ErrAuthStoreAvailable)
	}
	deviceDigest := digest(deviceCode)
	stored, err := s.cache.SetIfAbsent(ctx, "cli:user-code:"+userCode, []byte(deviceDigest), deviceCodeTTL)
	if err != nil || !stored {
		slog.ErrorContext(ctx, "failed to store device user code", "error", err, "stored", stored)
		return nil, rpcerr.New(connect.CodeUnavailable, ErrAuthStoreAvailable)
	}
	if err := s.cache.Set(ctx, deviceKey(deviceDigest), payload, deviceCodeTTL); err != nil {
		slog.ErrorContext(ctx, "failed to store device code", "error", err)
		return nil, rpcerr.New(connect.CodeUnavailable, ErrAuthStoreAvailable)
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

func (s *AuthServer) ApproveDeviceLogin(
	ctx context.Context,
	req *authv1.ApproveDeviceLoginRequest,
) (*authv1.ApproveDeviceLoginResponse, error) {
	grant, err := s.webIdentity(ctx)
	if err != nil {
		return nil, err
	}
	userCode := normalizeUserCode(req.GetUserCode())
	if len(userCode) != userCodeLength {
		return nil, rpcerr.New(connect.CodeNotFound, ErrUserCodeInvalid)
	}
	deviceDigest, err := s.cache.Get(ctx, "cli:user-code:"+userCode)
	if errors.Is(err, cache.ErrNotFound) {
		return nil, rpcerr.New(connect.CodeNotFound, ErrUserCodeInvalid)
	}
	if err != nil {
		return nil, rpcerr.New(connect.CodeUnavailable, ErrAuthStoreAvailable)
	}
	took, err := s.cache.Take(ctx, "cli:user-code:"+userCode)
	if err != nil || !took {
		return nil, rpcerr.New(connect.CodeNotFound, ErrUserCodeInvalid)
	}
	key := deviceKey(string(deviceDigest))
	raw, err := s.cache.Get(ctx, key)
	if err != nil {
		return nil, rpcerr.New(connect.CodeNotFound, ErrUserCodeInvalid)
	}
	var device deviceGrant
	if decodeErr := json.Unmarshal(raw, &device); decodeErr != nil {
		return nil, rpcerr.New(connect.CodeInternal, ErrAuthStoreAvailable)
	}
	remaining := device.ExpiresAt.Sub(s.now())
	if remaining <= 0 {
		return nil, rpcerr.New(connect.CodeNotFound, ErrUserCodeInvalid)
	}
	device.UserID = &grant.UserID
	device.IdentityID = &grant.IdentityID
	payload, err := json.Marshal(device)
	if err != nil {
		return nil, rpcerr.New(connect.CodeInternal, ErrAuthStoreAvailable)
	}
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if recordErr := events.Record(ctx, qtx, events.Event{
			Type:        events.CLILoginApproved,
			SubjectType: events.SubjectUser,
			SubjectID:   new(grant.UserID),
			Data:        map[string]any{"flow": "device"},
		}); recordErr != nil {
			return recordErr
		}
		return s.cache.Set(ctx, key, payload, remaining)
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to approve device login", "error", err)
		return nil, rpcerr.New(connect.CodeUnavailable, ErrAuthStoreAvailable)
	}
	slog.InfoContext(ctx, "approved device login", "userId", grant.UserID)
	return &authv1.ApproveDeviceLoginResponse{}, nil
}

func (s *AuthServer) PollDeviceLogin(
	ctx context.Context,
	req *authv1.PollDeviceLoginRequest,
) (*authv1.PollDeviceLoginResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	deviceDigest := digest(req.GetDeviceCode())
	key := deviceKey(deviceDigest)
	raw, err := s.cache.Get(ctx, key)
	if errors.Is(err, cache.ErrNotFound) {
		return nil, rpcerr.New(connect.CodeNotFound, ErrDeviceCodeExpired)
	}
	if err != nil {
		return nil, rpcerr.New(connect.CodeUnavailable, ErrAuthStoreAvailable)
	}
	var device deviceGrant
	if decodeErr := json.Unmarshal(raw, &device); decodeErr != nil {
		return nil, rpcerr.New(connect.CodeInternal, ErrAuthStoreAvailable)
	}
	if device.UserID == nil || device.IdentityID == nil {
		if throttleErr := s.throttleDevicePoll(ctx, deviceDigest); throttleErr != nil {
			return nil, throttleErr
		}
		return &authv1.PollDeviceLoginResponse{}, nil
	}
	took, err := s.cache.Take(ctx, key)
	if err != nil || !took {
		return nil, rpcerr.New(connect.CodeNotFound, ErrDeviceCodeExpired)
	}
	tokens, err := s.issue(ctx, cliGrant{UserID: *device.UserID, IdentityID: *device.IdentityID}, info.RequestHeader())
	if err != nil {
		return nil, err
	}
	return &authv1.PollDeviceLoginResponse{Tokens: tokens}, nil
}

func (s *AuthServer) throttleDevicePoll(ctx context.Context, deviceDigest string) error {
	pollKey := devicePollKey(deviceDigest)
	now := s.now()
	last, err := s.cache.Get(ctx, pollKey)
	if err != nil && !errors.Is(err, cache.ErrNotFound) {
		return rpcerr.New(connect.CodeUnavailable, ErrAuthStoreAvailable)
	}
	if err == nil {
		var lastPoll time.Time
		if decodeErr := lastPoll.UnmarshalText(last); decodeErr != nil {
			return rpcerr.New(connect.CodeInternal, ErrAuthStoreAvailable)
		}
		if now.Sub(lastPoll) < devicePollInterval-time.Second {
			return rpcerr.New(connect.CodeResourceExhausted, ErrDevicePollTooFast)
		}
	}
	stamp, err := now.MarshalText()
	if err != nil {
		return rpcerr.New(connect.CodeInternal, ErrAuthStoreAvailable)
	}
	if setErr := s.cache.Set(ctx, pollKey, stamp, devicePollInterval); setErr != nil {
		slog.WarnContext(ctx, "failed to record device poll", "error", setErr)
	}
	return nil
}

func (s *AuthServer) RefreshCLIToken(
	ctx context.Context,
	req *authv1.RefreshCLITokenRequest,
) (*authv1.RefreshCLITokenResponse, error) {
	refresh := req.GetRefreshToken()
	session, err := s.machine.SessionIdentity(ctx, refresh)
	switch {
	case err == nil:
		if revokeErr := s.checkIdentity(ctx, session); revokeErr != nil {
			return nil, revokeErr
		}
	case errors.Is(err, pgx.ErrNoRows):
	default:
		slog.ErrorContext(ctx, "failed to look up session identity", "error", err)
		return nil, rpcerr.New(connect.CodeInternal, ErrDB)
	}

	access, newRefresh, err := s.machine.Refresh(ctx, refresh)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, publicUnauthorized).WithCause(err)
	}
	return &authv1.RefreshCLITokenResponse{Tokens: &authv1.CLITokens{
		AccessToken:  access,
		RefreshToken: newRefresh,
		ExpiresIn:    int64(s.machine.Cfg.SessionAccessTokenDuration.Seconds()),
	}}, nil
}

func (s *AuthServer) checkIdentity(ctx context.Context, session genDb.GetSessionIdentityByRefreshHashRow) error {
	admin, ok := s.admins.For(session.Issuer)
	if !ok {
		return nil
	}
	state, err := admin.State(ctx, session.Subject)
	if err != nil {
		slog.WarnContext(ctx, "could not check identity with its provider; allowing refresh",
			"issuer", session.Issuer, "error", err)
		return nil
	}
	if state == auth.IdentityActive {
		return nil
	}
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if revokeErr := s.machine.WithQueries(qtx).RevokeIdentitySessions(ctx, session.IdentityID); revokeErr != nil {
			return revokeErr
		}
		return events.Record(ctx, qtx, events.Event{
			Type:        events.IdentityRevoked,
			ActorType:   events.ActorSystem,
			SubjectType: events.SubjectUser,
			SubjectID:   new(session.UserID),
			Data:        map[string]any{"issuer": session.Issuer, "reason": "disabled at identity provider"},
		})
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to revoke sessions of a disabled identity", "error", err)
		return rpcerr.New(connect.CodeUnavailable, ErrAuthStoreAvailable)
	}
	slog.InfoContext(ctx, "refresh refused for a disabled identity", "userId", session.UserID, "issuer", session.Issuer)
	return rpcerr.New(connect.CodeUnauthenticated, ErrSignInRevoked)
}
