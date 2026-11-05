package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"filippo.io/age"
)

var errRecoveryDrill = errors.New("encrypted recovery restore drill failed")

const (
	recoveryRequestTimeout = 30 * time.Second
	recoveryCommand        = "recovery"
	recoverySealKeyBytes   = 32
)

type baoClient struct {
	client          *http.Client
	endpoint, token string
}

type exportedMaterial struct {
	SealKey           string `json:"sealKey"`
	GenerateRootToken string `json:"generateRootToken"`
}

func recoveryDrill(ctx context.Context) (err error) {
	directory, err := os.MkdirTemp("", "loco-recovery-")
	if err != nil {
		return err
	}
	defer func() {
		if cleanupErr := os.RemoveAll(directory); cleanupErr != nil {
			err = errors.Join(err, errRecoveryDrill)
		}
	}()
	// Only ciphertext is written here; all private identities and decrypted material stay in memory.
	output := filepath.Join(directory, "encrypted")
	init, err := kubectl(ctx, "-n", controlNamespace, "get", "secret", "loco-control-plane-openbao-init", "-o", "json")
	if err != nil {
		return err
	}
	var source struct {
		Data map[string][]byte `json:"data"`
	}
	if err = json.Unmarshal(init, &source); err != nil {
		return errRecoveryDrill
	}
	count := len(strings.Fields(string(source.Data["recovery-keys"])))
	if count == 0 {
		return errRecoveryDrill
	}
	identities := make([]*age.X25519Identity, count+1)
	args := []string{recoveryCommand, "export", "--file", clientFile, "--output", output}
	for i := range identities {
		identities[i], err = age.GenerateX25519Identity()
		if err != nil {
			return errRecoveryDrill
		}
		flag := "--share-recipient"
		if i == 0 {
			flag = "--recipient"
		}
		args = append(args, flag, identities[i].Recipient().String())
	}
	if _, err = locoadm(ctx, args...); err != nil {
		return err
	}
	status, err := locoadm(ctx, "status", "--file", clientFile)
	if err != nil {
		return err
	}
	var exported struct {
		RecoveryExported bool `json:"recoveryExported"`
	}
	if json.Unmarshal(status, &exported) != nil || !exported.RecoveryExported {
		return errRecoveryDrill
	}
	material, shares, err := decryptRecovery(output, identities)
	if err != nil {
		return err
	}
	return restoreRecovery(ctx, material, shares)
}

func decryptRecovery(directory string, identities []*age.X25519Identity) (exportedMaterial, []string, error) {
	var material exportedMaterial
	shares := make([]string, 0, len(identities)-1)
	for i, identity := range identities {
		name := "material.age"
		if i > 0 {
			name = fmt.Sprintf("share-%d.age", i)
		}
		ciphertext, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			return material, nil, errRecoveryDrill
		}
		decrypted, err := age.Decrypt(bytes.NewReader(ciphertext), identity)
		if err != nil {
			return material, nil, errRecoveryDrill
		}
		plaintext, err := io.ReadAll(decrypted)
		if err != nil {
			return material, nil, errRecoveryDrill
		}
		if i == 0 {
			if err = json.Unmarshal(plaintext, &material); err != nil {
				return material, nil, errRecoveryDrill
			}
		} else {
			shares = append(shares, string(plaintext))
		}
	}
	seal, err := base64.StdEncoding.DecodeString(material.SealKey)
	if err != nil || len(seal) != recoverySealKeyBytes || material.GenerateRootToken == "" || len(shares) == 0 {
		return material, nil, errRecoveryDrill
	}
	return material, shares, nil
}

func (b baoClient) request(ctx context.Context, method, path string, input io.Reader) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, method, b.endpoint+"/v1/"+path, input)
	if err != nil {
		return nil, errRecoveryDrill
	}
	request.Header.Set("X-Vault-Token", b.token)
	request.Header.Set("Content-Type", "application/json")
	response, err := b.client.Do(request)
	if err != nil {
		return nil, errRecoveryDrill
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("OpenBao recovery request failed: HTTP %d", response.StatusCode)
	}
	result, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, errRecoveryDrill
	}
	return result, nil
}

func (b baoClient) write(ctx context.Context, path string, input, output any) error {
	body, err := json.Marshal(input)
	if err != nil {
		return errRecoveryDrill
	}
	data, err := b.request(ctx, http.MethodPost, path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	if output != nil && json.Unmarshal(data, output) != nil {
		return errRecoveryDrill
	}
	return nil
}

func (b baoClient) recoverRoot(ctx context.Context, shares []string) (token string, err error) {
	var attempt struct {
		Data struct{ Nonce, OTP string } `json:"data"`
	}
	if err = b.write(ctx, "sys/generate-root-token/attempt", map[string]string{}, &attempt); err != nil {
		return "", err
	}
	defer func() {
		if token == "" {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
			defer cancel()
			_, cancelErr := b.request(cleanup, http.MethodDelete, "sys/generate-root-token/attempt", nil)
			err = errors.Join(err, cancelErr)
		}
	}()
	if attempt.Data.Nonce == "" || attempt.Data.OTP == "" {
		return "", errRecoveryDrill
	}
	for _, share := range shares {
		var progress struct {
			Data struct {
				Complete bool
				Encoded  string `json:"encoded_token"`
			} `json:"data"`
		}
		if err := b.write(
			ctx,
			"sys/generate-root-token/update",
			map[string]string{"nonce": attempt.Data.Nonce, "key": share},
			&progress,
		); err != nil {
			return "", err
		}
		if progress.Data.Complete {
			decoded, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(progress.Data.Encoded, "="))
			if err != nil || len(decoded) != len(attempt.Data.OTP) {
				return "", errRecoveryDrill
			}
			for i := range decoded {
				decoded[i] ^= attempt.Data.OTP[i]
			}
			return string(decoded), nil
		}
	}
	return "", errRecoveryDrill
}

// The plaintext and ciphertext predate the snapshot; the restored ingest key must retain its public identity.
func recoveryProof(
	ctx context.Context,
	source, restored baoClient,
	snapshot func(context.Context) error,
	transit, ingest string,
) error {
	const canary = "installation-recovery-canary"
	var encrypted struct{ Data struct{ Ciphertext string } }
	// Transit routes are <mount>/encrypt/<key>, supplied from the installed API's configuration.
	if err := source.write(
		ctx,
		transit,
		map[string]string{"plaintext": base64.StdEncoding.EncodeToString([]byte(canary))},
		&encrypted,
	); err != nil {
		return err
	}
	if encrypted.Data.Ciphertext == "" {
		return errRecoveryDrill
	}
	public, err := source.request(ctx, http.MethodGet, strings.Replace(ingest, "/sign/", "/keys/", 1), nil)
	if err != nil {
		return err
	}
	var keys struct {
		Data struct {
			Latest int `json:"latest_version"`
			Keys   map[string]struct {
				Public string `json:"public_key"`
			} `json:"keys"`
		} `json:"data"`
	}
	if json.Unmarshal(public, &keys) != nil {
		return errRecoveryDrill
	}
	key, err := base64.StdEncoding.DecodeString(keys.Data.Keys[strconv.Itoa(keys.Data.Latest)].Public)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return errRecoveryDrill
	}
	if err = snapshot(ctx); err != nil {
		return err
	}
	var decrypted struct{ Data struct{ Plaintext string } }
	if err = restored.write(
		ctx,
		strings.Replace(transit, "/encrypt/", "/decrypt/", 1),
		map[string]string{"ciphertext": encrypted.Data.Ciphertext},
		&decrypted,
	); err != nil {
		return err
	}
	if decrypted.Data.Plaintext != base64.StdEncoding.EncodeToString([]byte(canary)) {
		return errRecoveryDrill
	}
	var signed struct{ Data struct{ Signature string } }
	if err = restored.write(
		ctx,
		ingest,
		map[string]string{"input": base64.StdEncoding.EncodeToString([]byte(canary))},
		&signed,
	); err != nil {
		return err
	}
	fields := strings.Split(signed.Data.Signature, ":")
	if len(fields) != 3 {
		return errRecoveryDrill
	}
	signature, err := base64.StdEncoding.DecodeString(fields[2])
	if err != nil || !ed25519.Verify(ed25519.PublicKey(key), []byte(canary), signature) {
		return errRecoveryDrill
	}
	return nil
}

func denyRecoveryRedirect(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
