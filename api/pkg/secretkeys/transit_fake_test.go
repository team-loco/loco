package secretkeys

import (
	"bytes"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

var errFakeMissingContext = errors.New("missing 'context' for key derivation")

const (
	fakeTransitMount = "transit"
	fakeTransitKey   = "loco"
	fakeTransitToken = "s.fake-token"
	fakeSigningKey   = "loco-ingest"

	transitTypeField      = "type"
	transitRenewableField = "renewable"
	fakeLookupSelfPath    = "/v1/auth/token/lookup-self"
	fakeRenewSelfPath     = "/v1/auth/token/renew-self"
	fakeKubernetesMount   = "kubernetes"
	fakeKubernetesRole    = "loco-api"
	fakeLoginPath         = "/v1/auth/" + fakeKubernetesMount + "/login"

	transitFakeNonceSize = 12
)

type fakeTransit struct {
	t *testing.T

	mu      sync.Mutex
	keys    [][]byte
	derived bool
	keyType string
	token   string

	signingKeys       []ed25519.PrivateKey
	signingKeyType    string
	minSigningVersion int
	requests          map[string]int
	block             chan struct{}

	tokenTTL       time.Duration
	tokenRenewable bool
	renewFails     bool
	jwt            string
	logins         int
	loginTokens    map[string]bool
}

func newFakeTransit(t *testing.T) *fakeTransit {
	t.Helper()
	fake := &fakeTransit{
		t:        t,
		derived:  true,
		keyType:  transitKeyType,
		token:    fakeTransitToken,
		requests: map[string]int{},

		loginTokens: map[string]bool{},

		tokenRenewable: true,

		signingKeyType:    transitSigningKeyType,
		minSigningVersion: 1,
	}
	fake.rotate()
	fake.rotateSigningKey()
	return fake
}

func (f *fakeTransit) rotateSigningKey() {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		f.t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.signingKeys = append(f.signingKeys, key)
}

func (f *fakeTransit) trimSigningKeys(minVersion int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.minSigningVersion = minVersion
}

func (f *fakeTransit) setSigningKeyType(keyType string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.signingKeyType = keyType
}

func (f *fakeTransit) readSigningKey(w http.ResponseWriter) {
	f.mu.Lock()
	defer f.mu.Unlock()
	keys := map[string]any{}
	for index, key := range f.signingKeys[f.minSigningVersion-1:] {
		public, ok := key.Public().(ed25519.PublicKey)
		if !ok {
			f.t.Fatal("ed25519 key without an ed25519 public key")
		}
		version := strconv.Itoa(f.minSigningVersion + index)
		keys[version] = map[string]any{"public_key": base64.StdEncoding.EncodeToString(public)}
	}
	f.writeData(w, map[string]any{
		transitTypeField:         f.signingKeyType,
		"latest_version":         len(f.signingKeys),
		"min_decryption_version": f.minSigningVersion,
		"keys":                   keys,
	})
}

func (f *fakeTransit) sign(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Input      string `json:"input"`
		KeyVersion int    `json:"key_version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		f.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	input, err := base64.StdEncoding.DecodeString(body.Input)
	if err != nil {
		f.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if body.KeyVersion < f.minSigningVersion || body.KeyVersion > len(f.signingKeys) {
		f.writeError(w, http.StatusBadRequest, "invalid key version")
		return
	}
	signature := ed25519.Sign(f.signingKeys[body.KeyVersion-1], input)
	encoded := base64.StdEncoding.EncodeToString(signature)
	f.writeData(w, map[string]any{"signature": fmt.Sprintf("vault:v%d:%s", body.KeyVersion, encoded)})
}

func (f *fakeTransit) rotate() {
	f.mu.Lock()
	defer f.mu.Unlock()
	version := byte(len(f.keys) + 1)
	f.keys = append(f.keys, bytes.Repeat([]byte{version}, KeySize))
}

func (f *fakeTransit) count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[path]
}

func (f *fakeTransit) server() *httptest.Server {
	server := httptest.NewServer(f)
	f.t.Cleanup(server.Close)
	return server
}

func (f *fakeTransit) tlsServer() *httptest.Server {
	server := httptest.NewTLSServer(f)
	f.t.Cleanup(server.Close)
	return server
}

func (f *fakeTransit) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests[r.URL.Path]++
	block := f.block
	token := f.token
	loggedIn := f.loginTokens[r.Header.Get(transitAuthHeader)]
	f.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-r.Context().Done():
			return
		}
	}
	if r.Method == http.MethodPost && r.URL.Path == fakeLoginPath {
		f.login(w, r)
		return
	}
	if r.Header.Get(transitAuthHeader) != token && !loggedIn {
		f.writeError(w, http.StatusForbidden, "permission denied")
		return
	}
	prefix := "/v1/" + fakeTransitMount + "/"
	switch {
	case r.Method == http.MethodGet && r.URL.Path == fakeLookupSelfPath:
		f.lookupSelf(w)
	case r.Method == http.MethodPost && r.URL.Path == fakeRenewSelfPath:
		f.renewSelf(w)
	case r.Method == http.MethodGet && r.URL.Path == prefix+"keys/"+fakeTransitKey:
		f.readKey(w)
	case r.Method == http.MethodPost && r.URL.Path == prefix+"encrypt/"+fakeTransitKey:
		f.encrypt(w, r)
	case r.Method == http.MethodPost && r.URL.Path == prefix+"decrypt/"+fakeTransitKey:
		f.decrypt(w, r)
	case r.Method == http.MethodGet && r.URL.Path == prefix+"keys/"+fakeSigningKey:
		f.readSigningKey(w)
	case r.Method == http.MethodPost && r.URL.Path == prefix+"sign/"+fakeSigningKey:
		f.sign(w, r)
	default:
		f.writeError(w, http.StatusNotFound, "no handler for route")
	}
}

func (f *fakeTransit) setToken(ttl time.Duration, renewable bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokenTTL = ttl
	f.tokenRenewable = renewable
}

func (f *fakeTransit) currentToken() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.token
}

func (f *fakeTransit) forgetToken() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.token = "s.forgotten"
	f.loginTokens = map[string]bool{}
}

func (f *fakeTransit) acceptJWT(jwt string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.jwt = jwt
}

func (f *fakeTransit) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Role string `json:"role"`
		JWT  string `json:"jwt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		f.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get(transitAuthHeader) != "" || body.Role != fakeKubernetesRole || f.jwt == "" || body.JWT != f.jwt {
		f.writeError(w, http.StatusForbidden, "permission denied")
		return
	}
	f.logins++
	f.token = fmt.Sprintf("s.login-%d", f.logins)
	f.loginTokens[f.token] = true
	f.writeJSON(w, http.StatusOK, map[string]any{"auth": map[string]any{
		"client_token":   f.token,
		"lease_duration": int(f.tokenTTL.Seconds()),
	}})
}

func (f *fakeTransit) setRenewFails(fails bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.renewFails = fails
}

func (f *fakeTransit) lookupSelf(w http.ResponseWriter) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writeData(w, map[string]any{
		"ttl":                 int(f.tokenTTL.Seconds()),
		transitRenewableField: f.tokenRenewable,
	})
}

func (f *fakeTransit) renewSelf(w http.ResponseWriter) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.renewFails {
		f.writeError(w, http.StatusInternalServerError, "storage unavailable")
		return
	}
	f.writeJSON(w, http.StatusOK, map[string]any{"auth": map[string]any{
		"lease_duration":      int(f.tokenTTL.Seconds()),
		transitRenewableField: f.tokenRenewable,
	}})
}

func (f *fakeTransit) readKey(w http.ResponseWriter) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writeData(w, map[string]any{
		transitTypeField: f.keyType,
		"derived":        f.derived,
		"latest_version": len(f.keys),
	})
}

func (f *fakeTransit) derive(version int, context string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if version < 1 || version > len(f.keys) {
		return nil, fmt.Errorf("invalid key version %d", version)
	}
	if f.derived && context == "" {
		return nil, errFakeMissingContext
	}
	mac := hmac.New(sha256.New, f.keys[version-1])
	mac.Write([]byte(context))
	return mac.Sum(nil), nil
}

func (f *fakeTransit) encrypt(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Plaintext string `json:"plaintext"`
		Context   string `json:"context"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		f.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	plaintext, err := base64.StdEncoding.DecodeString(body.Plaintext)
	if err != nil {
		f.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	f.mu.Lock()
	version := len(f.keys)
	f.mu.Unlock()
	key, err := f.derive(version, body.Context)
	if err != nil {
		f.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	nonce, sealed, err := Seal(key, plaintext, nil)
	if err != nil {
		f.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	encoded := base64.StdEncoding.EncodeToString(append(nonce, sealed...))
	ciphertext := fmt.Sprintf("vault:v%d:%s", version, encoded)
	f.writeData(w, map[string]any{"ciphertext": ciphertext, "key_version": version})
}

func (f *fakeTransit) decrypt(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Ciphertext string `json:"ciphertext"`
		Context    string `json:"context"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		f.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	parts := strings.SplitN(body.Ciphertext, ":", 3)
	if len(parts) != 3 || parts[0] != "vault" || !strings.HasPrefix(parts[1], "v") {
		f.writeError(w, http.StatusBadRequest, "invalid ciphertext")
		return
	}
	version, err := strconv.Atoi(strings.TrimPrefix(parts[1], "v"))
	if err != nil {
		f.writeError(w, http.StatusBadRequest, "invalid ciphertext version")
		return
	}
	key, err := f.derive(version, body.Context)
	if err != nil {
		f.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	raw, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil || len(raw) < transitFakeNonceSize {
		f.writeError(w, http.StatusBadRequest, "invalid ciphertext encoding")
		return
	}
	plaintext, err := Open(key, raw[:transitFakeNonceSize], raw[transitFakeNonceSize:], nil)
	if err != nil {
		f.writeError(w, http.StatusBadRequest, "cipher: message authentication failed")
		return
	}
	encoded := base64.StdEncoding.EncodeToString(plaintext)
	f.writeData(w, map[string]any{"plaintext": encoded})
}

func (f *fakeTransit) writeData(w http.ResponseWriter, data map[string]any) {
	f.writeJSON(w, http.StatusOK, map[string]any{"data": data})
}

func (f *fakeTransit) writeError(w http.ResponseWriter, status int, message string) {
	f.writeJSON(w, status, map[string]any{"errors": []string{message}})
}

func (f *fakeTransit) writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		f.t.Errorf("encode fake transit response: %v", err)
	}
}
