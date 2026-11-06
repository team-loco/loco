package installation

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"filippo.io/age"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	errRecoveryRecipients = errors.New(
		"provide one distinct age X25519 recipient per recovery share and a separate material recipient",
	)
	errRecoveryExport = errors.New("encrypted recovery export failed; use a new output directory when retrying")
)

type RecoveryExport struct {
	Output          string
	Recipient       string
	ShareRecipients []string
}

type recoveryMaterial struct {
	SealKey           string `json:"sealKey"`
	GenerateRootToken string `json:"generateRootToken"`
}

// ExportRecovery leaves source material intact until an operator verifies recovery.
func ExportRecovery(ctx context.Context, config Config, options RecoveryExport) error {
	kube, err := NewKubernetes(config.ControlPlane.Kubernetes)
	if err != nil {
		return err
	}
	state, err := kube.state(ctx, config.Name)
	if err != nil {
		return err
	}
	return kube.withLock(ctx, state, func(ctx context.Context, state *corev1.ConfigMap) error {
		return kube.exportRecovery(ctx, state, options)
	})
}

func (kube *Kubernetes) exportRecovery(ctx context.Context, state *corev1.ConfigMap, options RecoveryExport) error {
	seal, err := kube.Core.Secrets(kube.Target.Namespace).Get(ctx, sealSecretName, metav1.GetOptions{})
	if err != nil {
		return safeKubernetesError("read recovery seal material", err)
	}
	if seal.Annotations[installationID] != string(state.UID) {
		return errForeignOwnership
	}
	if !validSeal(seal) {
		return errSecretInvalid
	}
	init, err := kube.Core.Secrets(kube.Target.Namespace).
		Get(ctx, controlPlaneRelease+"-openbao-init", metav1.GetOptions{})
	if err != nil {
		return safeKubernetesError("read recovery initialization material", err)
	}
	shares := strings.Fields(string(init.Data["recovery-keys"]))
	if len(shares) == 0 || len(init.Data["generate-root-token"]) == 0 {
		return errSecretInvalid
	}
	for _, share := range shares {
		if _, err := base64.StdEncoding.DecodeString(share); err != nil {
			return errSecretInvalid
		}
	}
	material := recoveryMaterial{
		SealKey:           string(seal.Data[secretKeyField]),
		GenerateRootToken: string(init.Data["generate-root-token"]),
	}
	if err := writeRecovery(options, material, shares); err != nil {
		return err
	}
	state.Data["recoveryExported"] = stateTrue
	if _, err := kube.Core.ConfigMaps(kube.Target.Namespace).Update(ctx, state, metav1.UpdateOptions{}); err != nil {
		return safeKubernetesError("record encrypted recovery export", err)
	}
	return nil
}

func writeRecovery(options RecoveryExport, material recoveryMaterial, shares []string) error {
	if len(shares) == 0 || len(options.ShareRecipients) != len(shares) || options.Output == "" {
		return errRecoveryRecipients
	}
	owner, err := age.ParseX25519Recipient(options.Recipient)
	if err != nil {
		return errRecoveryRecipients
	}
	recipients := []*age.X25519Recipient{owner}
	seen := map[string]bool{owner.String(): true}
	for _, value := range options.ShareRecipients {
		recipient, parseErr := age.ParseX25519Recipient(value)
		if parseErr != nil || seen[recipient.String()] {
			return errRecoveryRecipients
		}
		seen[recipient.String()] = true
		recipients = append(recipients, recipient)
	}
	data, err := json.Marshal(material)
	if err != nil {
		return errRecoveryExport
	}
	plaintext := [][]byte{data}
	for _, share := range shares {
		plaintext = append(plaintext, []byte(share))
	}
	// Encrypt every payload before creating the destination; no plaintext reaches disk.
	ciphertexts := make([][]byte, len(plaintext))
	for i, entry := range plaintext {
		var buffer bytes.Buffer
		encrypted, err := age.Encrypt(&buffer, recipients[i])
		if err != nil {
			return errRecoveryExport
		}
		if _, err = encrypted.Write(entry); err != nil {
			return errRecoveryExport
		}
		if err = encrypted.Close(); err != nil {
			return errRecoveryExport
		}
		ciphertexts[i] = buffer.Bytes()
	}
	if err := os.Mkdir(options.Output, 0700); err != nil {
		return errRecoveryExport
	}
	for i, ciphertext := range ciphertexts {
		name := "material.age"
		if i > 0 {
			name = fmt.Sprintf("share-%d.age", i)
		}
		if err := writeRecoveryFile(filepath.Join(options.Output, name), ciphertext); err != nil {
			return err
		}
	}
	for _, path := range []string{options.Output, filepath.Dir(options.Output)} {
		directory, openErr := os.Open(path)
		if openErr != nil {
			return errRecoveryExport
		}
		syncErr := directory.Sync()
		closeErr := directory.Close()
		if syncErr != nil || closeErr != nil {
			return errRecoveryExport
		}
	}
	return nil
}

func writeRecoveryFile(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errRecoveryExport
	}
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return errRecoveryExport
	}
	return nil
}
