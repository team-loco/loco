// Package transittest serves a fake Transit secrets engine for tests: token lookup and
// renewal, Kubernetes login, one derived aes256-gcm96 key and one ed25519 signing key.
package transittest

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
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

var errMissingContext = errors.New("missing 'context' for key derivation")

const (
	Mount           = "transit"
	Key             = "loco"
	Token           = "s.fake-token"
	SigningKey      = "loco-ingest"
	KubernetesMount = "kubernetes"
	KubernetesRole  = "loco-api"
	LookupSelfPath  = "/v1/auth/token/lookup-self"
	RenewSelfPath   = "/v1/auth/token/renew-self"
	LoginPath       = "/v1/auth/" + KubernetesMount + "/login"
	KeyPath         = "/v1/" + Mount + "/keys/" + Key
	EncryptPath     = "/v1/" + Mount + "/encrypt/" + Key
	DecryptPath     = "/v1/" + Mount + "/decrypt/" + Key
	SigningKeyPath  = "/v1/" + Mount + "/keys/" + SigningKey

	// Kept apart from the transit package so the fake follows the protocol, not the client.
	authHeader        = "X-Vault-Token"
	encryptionKeyType = "aes256-gcm96"
	signingKeyType    = "ed25519"
	typeField         = "type"
	renewableField    = "renewable"
	keySize           = 32
)

type Fake struct {
	t *testing.T

	mu      sync.Mutex
	keys    [][]byte
	derived bool
	keyType string
	token   string

	signingKeys       []ed25519.PrivateKey
	signingKeyType    string
	minSigningVersion int
	minEncryptVersion int
	requests          map[string]int
	block             chan struct{}
	unavailable       int

	tokenTTL       time.Duration
	tokenRenewable bool
	renewFails     bool
	jwt            string
	logins         int
	loginTokens    map[string]bool
}

func New(t *testing.T) *Fake {
	t.Helper()
	fake := &Fake{
		t:        t,
		derived:  true,
		keyType:  encryptionKeyType,
		token:    Token,
		requests: map[string]int{},

		loginTokens: map[string]bool{},

		tokenRenewable: true,

		signingKeyType:    signingKeyType,
		minSigningVersion: 1,
	}
	fake.Rotate()
	fake.RotateSigningKey()
	return fake
}

func (f *Fake) RotateSigningKey() {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		f.t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.signingKeys = append(f.signingKeys, key)
}

func (f *Fake) TrimSigningKeys(minVersion int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.minSigningVersion = minVersion
}

func (f *Fake) RaiseMinEncryptionVersion(minVersion int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.minEncryptVersion = minVersion
}

func (f *Fake) SetKeyType(keyType string, derived bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keyType = keyType
	f.derived = derived
}

func (f *Fake) DropSigningKeys() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.signingKeys = nil
}

// Block holds every request until the test ends or the request ends.
func (f *Fake) Block() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.block = make(chan struct{})
	f.t.Cleanup(func() { close(f.block) })
}

func (f *Fake) SetSigningKeyType(keyType string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.signingKeyType = keyType
}

func (f *Fake) readSigningKey(w http.ResponseWriter) {
	f.mu.Lock()
	defer f.mu.Unlock()
	keys := map[string]any{}
	for index, key := range f.signingKeys[f.minSigningVersion-1:] {
		public, ok := key.Public().(ed25519.PublicKey)
		if !ok {
			f.t.Fatal("ed25519 key without an ed25519 public key")
		}
		version := strconv.Itoa(f.minSigningVersion + index)
		if f.signingKeyType != signingKeyType {
			keys[version] = time.Now().Unix()
			continue
		}
		keys[version] = map[string]any{"public_key": base64.StdEncoding.EncodeToString(public)}
	}
	f.writeData(w, map[string]any{
		typeField:                f.signingKeyType,
		"latest_version":         len(f.signingKeys),
		"min_decryption_version": f.minSigningVersion,
		"min_encryption_version": f.minEncryptVersion,
		"keys":                   keys,
	})
}

func (f *Fake) sign(w http.ResponseWriter, r *http.Request) {
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
	if body.KeyVersion < max(f.minSigningVersion, f.minEncryptVersion) || body.KeyVersion > len(f.signingKeys) {
		f.writeError(w, http.StatusBadRequest, "invalid key version")
		return
	}
	signature := ed25519.Sign(f.signingKeys[body.KeyVersion-1], input)
	encoded := base64.StdEncoding.EncodeToString(signature)
	f.writeData(w, map[string]any{"signature": fmt.Sprintf("vault:v%d:%s", body.KeyVersion, encoded)})
}

func (f *Fake) Rotate() {
	f.mu.Lock()
	defer f.mu.Unlock()
	version := byte(len(f.keys) + 1)
	f.keys = append(f.keys, bytes.Repeat([]byte{version}, keySize))
}

func (f *Fake) Count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[path]
}

func (f *Fake) Server() *httptest.Server {
	server := httptest.NewServer(f)
	f.t.Cleanup(server.Close)
	return server
}

func (f *Fake) TLSServer() *httptest.Server {
	server := httptest.NewTLSServer(f)
	f.t.Cleanup(server.Close)
	return server
}

func (f *Fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests[r.URL.Path]++
	block := f.block
	token := f.token
	loggedIn := f.loginTokens[r.Header.Get(authHeader)]
	sealed := f.unavailable > 0
	if sealed {
		f.unavailable--
	}
	f.mu.Unlock()
	if sealed {
		f.writeError(w, http.StatusServiceUnavailable, "Vault is sealed")
		return
	}
	if block != nil {
		select {
		case <-block:
		case <-r.Context().Done():
			return
		}
	}
	if r.Method == http.MethodPost && r.URL.Path == LoginPath {
		f.login(w, r)
		return
	}
	if r.Header.Get(authHeader) != token && !loggedIn {
		f.writeError(w, http.StatusForbidden, "permission denied")
		return
	}
	prefix := "/v1/" + Mount + "/"
	switch {
	case r.Method == http.MethodGet && r.URL.Path == LookupSelfPath:
		f.lookupSelf(w)
	case r.Method == http.MethodPost && r.URL.Path == RenewSelfPath:
		f.renewSelf(w)
	case r.Method == http.MethodGet && r.URL.Path == prefix+"keys/"+Key:
		f.readKey(w)
	case r.Method == http.MethodPost && r.URL.Path == prefix+"encrypt/"+Key:
		f.encrypt(w, r)
	case r.Method == http.MethodPost && r.URL.Path == prefix+"decrypt/"+Key:
		f.decrypt(w, r)
	case r.Method == http.MethodGet && r.URL.Path == prefix+"keys/"+SigningKey:
		f.readSigningKey(w)
	case r.Method == http.MethodPost && r.URL.Path == prefix+"sign/"+SigningKey:
		f.sign(w, r)
	default:
		f.writeError(w, http.StatusNotFound, "no handler for route")
	}
}

func (f *Fake) FailRequests(count int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unavailable = count
}

func (f *Fake) SetToken(ttl time.Duration, renewable bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokenTTL = ttl
	f.tokenRenewable = renewable
}

func (f *Fake) CurrentToken() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.token
}

func (f *Fake) ForgetToken() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.token = "s.forgotten"
	f.loginTokens = map[string]bool{}
}

func (f *Fake) AcceptJWT(jwt string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.jwt = jwt
}

func (f *Fake) login(w http.ResponseWriter, r *http.Request) {
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
	if r.Header.Get(authHeader) != "" || body.Role != KubernetesRole || f.jwt == "" || body.JWT != f.jwt {
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

func (f *Fake) SetRenewFails(fails bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.renewFails = fails
}

func (f *Fake) lookupSelf(w http.ResponseWriter) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writeData(w, map[string]any{
		"ttl":          int(f.tokenTTL.Seconds()),
		renewableField: f.tokenRenewable,
	})
}

func (f *Fake) renewSelf(w http.ResponseWriter) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.renewFails {
		f.writeError(w, http.StatusInternalServerError, "storage unavailable")
		return
	}
	f.writeJSON(w, http.StatusOK, map[string]any{"auth": map[string]any{
		"lease_duration": int(f.tokenTTL.Seconds()),
		renewableField:   f.tokenRenewable,
	}})
}

func (f *Fake) readKey(w http.ResponseWriter) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writeData(w, map[string]any{
		typeField:        f.keyType,
		"derived":        f.derived,
		"latest_version": len(f.keys),
	})
}

func (f *Fake) derive(version int, context string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if version < 1 || version > len(f.keys) {
		return nil, fmt.Errorf("invalid key version %d", version)
	}
	if f.derived && context == "" {
		return nil, errMissingContext
	}
	mac := hmac.New(sha256.New, f.keys[version-1])
	mac.Write([]byte(context))
	return mac.Sum(nil), nil
}

func (f *Fake) encrypt(w http.ResponseWriter, r *http.Request) {
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
	gcm, err := newGCM(key)
	if err != nil {
		f.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		f.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	encoded := base64.StdEncoding.EncodeToString(gcm.Seal(nonce, nonce, plaintext, nil))
	ciphertext := fmt.Sprintf("vault:v%d:%s", version, encoded)
	f.writeData(w, map[string]any{"ciphertext": ciphertext, "key_version": version})
}

func (f *Fake) decrypt(w http.ResponseWriter, r *http.Request) {
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
	gcm, err := newGCM(key)
	if err != nil {
		f.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	raw, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil || len(raw) < gcm.NonceSize() {
		f.writeError(w, http.StatusBadRequest, "invalid ciphertext encoding")
		return
	}
	plaintext, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		f.writeError(w, http.StatusBadRequest, "cipher: message authentication failed")
		return
	}
	encoded := base64.StdEncoding.EncodeToString(plaintext)
	f.writeData(w, map[string]any{"plaintext": encoded})
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("new cipher: %w", err)
	}
	return cipher.NewGCM(block)
}

func (f *Fake) writeData(w http.ResponseWriter, data map[string]any) {
	f.writeJSON(w, http.StatusOK, map[string]any{"data": data})
}

func (f *Fake) writeError(w http.ResponseWriter, status int, message string) {
	f.writeJSON(w, status, map[string]any{"errors": []string{message}})
}

func (f *Fake) writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		f.t.Errorf("encode fake transit response: %v", err)
	}
}
