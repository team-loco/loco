package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"strconv"
	"time"
)

const (
	recoveryNamespace = "installation-restore-drill"
	sourceBao         = "loco-control-plane-openbao"
	recoveryPod       = "restore"
	// the disposable restore listener and advertised address share this port.
	recoveryBaoPort      = "8200"
	recoveryReadyTimeout = 2 * time.Minute
)

func restoreRecovery(ctx context.Context, material exportedMaterial, shares []string) error {
	var source, restored baoClient
	var sourceForward, forward *exec.Cmd
	var restoredInitialized bool
	// Source roots are revoked independently of acceptance cancellation, before fixture deletion.
	err := execute(ctx, func(ctx context.Context) error {
		configuration, err := kubectl(ctx, "-n", controlNamespace, "get", "configmap", sourceBao, "-o", "json")
		if err != nil {
			return err
		}
		var config struct {
			Data map[string]string `json:"data"`
		}
		if json.Unmarshal(configuration, &config) != nil {
			return errRecoveryDrill
		}
		keyID := regexp.MustCompile(`current_key_id\s*=\s*"([^"]+)"`).FindStringSubmatch(config.Data["config.hcl"])
		if len(keyID) != 2 {
			return errRecoveryDrill
		}
		pod, err := kubectl(ctx, "-n", controlNamespace, "get", "pod", sourceBao+"-0", "-o", "json")
		if err != nil {
			return err
		}
		var running struct {
			Spec struct {
				Containers []struct {
					Image string `json:"image"`
					Ports []struct {
						ContainerPort int    `json:"containerPort"`
						Name          string `json:"name"`
					} `json:"ports"`
				} `json:"containers"`
			} `json:"spec"`
		}
		if json.Unmarshal(pod, &running) != nil {
			return errRecoveryDrill
		}
		port := 0
		for _, item := range running.Spec.Containers[0].Ports {
			if item.Name == "http" {
				port = item.ContainerPort
			}
		}
		if port == 0 {
			return errRecoveryDrill
		}
		certificate, err := kubectl(ctx, "-n", controlNamespace, "get", "secret", sourceBao+"-tls", "-o", "json")
		if err != nil {
			return err
		}
		var secret struct {
			Data map[string][]byte `json:"data"`
		}
		if json.Unmarshal(certificate, &secret) != nil {
			return errRecoveryDrill
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(secret.Data["ca.crt"]) {
			return errRecoveryDrill
		}
		serverName := sourceBao + "." + controlNamespace + ".svc"
		source = baoClient{
			client: &http.Client{
				Timeout:       recoveryRequestTimeout,
				CheckRedirect: denyRecoveryRedirect,
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: serverName, MinVersion: tls.VersionTLS12},
				},
			},
			endpoint: "https://" + net.JoinHostPort(serverName, strconv.Itoa(port)),
			token:    material.GenerateRootToken,
		}
		defer source.client.CloseIdleConnections()
		sourceForward, source.endpoint, err = forwardBao(ctx, source, controlNamespace, "service/"+sourceBao)
		if err != nil {
			return err
		}
		source.token, err = source.recoverRoot(ctx, shares)
		if err != nil {
			return err
		}
		// The export credential cannot snapshot, decrypt or sign; only the quorum-derived root can.
		paths, err := recoveryTransitPaths(ctx)
		if err != nil {
			return err
		}
		if err = provisionRecovery(ctx, running.Spec.Containers[0].Image, keyID[1], material.SealKey); err != nil {
			return err
		}
		restored = baoClient{
			client:   &http.Client{Timeout: recoveryRequestTimeout, CheckRedirect: denyRecoveryRedirect},
			endpoint: "http://127.0.0.1:" + recoveryBaoPort,
			token:    source.token,
		}
		defer restored.client.CloseIdleConnections()
		forward, restored.endpoint, err = forwardBao(ctx, restored, recoveryNamespace, "pod/"+recoveryPod)
		if err != nil {
			return err
		}
		return recoveryProof(ctx, source, &restored, func(ctx context.Context) error {
			snapshot, err := source.request(ctx, http.MethodGet, "sys/storage/raft/snapshot", nil)
			if err != nil {
				return err
			}
			var initialized struct {
				Root string `json:"root_token"`
			}
			bootstrap := restored
			bootstrap.token = ""
			if err = bootstrap.write(
				ctx,
				"sys/init",
				map[string]int{"recovery_shares": 1, "recovery_threshold": 1},
				&initialized,
			); err != nil {
				return err
			}
			if initialized.Root == "" {
				return errRecoveryDrill
			}
			bootstrap.token = initialized.Root
			restored.token = initialized.Root
			restoredInitialized = true
			if err = waitBao(ctx, bootstrap); err != nil {
				return err
			}
			if _, err = bootstrap.request(
				ctx,
				http.MethodPost,
				"sys/storage/raft/snapshot-force",
				bytes.NewReader(snapshot),
			); err != nil {
				return err
			}
			restored.token = source.token
			forward, restored.endpoint, err = restartRestoredBao(ctx, restored, forward)
			if err != nil {
				return err
			}
			return waitBao(ctx, restored)
		}, paths[0], paths[1])
	}, func(ctx context.Context) error {
		var cleanup []error
		if source.token != "" && source.token != material.GenerateRootToken {
			_, err := source.request(ctx, http.MethodPost, "auth/token/revoke-self", nil)
			cleanup = append(cleanup, err)
		}
		if restoredInitialized {
			_, err := restored.request(ctx, http.MethodPost, "auth/token/revoke-self", nil)
			cleanup = append(cleanup, err)
		}
		for _, process := range []*exec.Cmd{sourceForward, forward} {
			if process != nil {
				cleanup = append(cleanup, stopProcess(process))
			}
		}
		_, err := kubectl(ctx, "delete", "namespace", recoveryNamespace, "--ignore-not-found", "--wait=true")
		return errors.Join(append(cleanup, err)...)
	})
	if err != nil {
		return err
	}
	fmt.Println("Encrypted recovery restore passed: existing Transit ciphertext and ingest signing identity recovered.")
	return nil
}

func recoveryTransitPaths(ctx context.Context) ([2]string, error) {
	var paths [2]string
	data, err := kubectl(ctx, "-n", controlNamespace, "get", "deployment", "loco-control-plane-api", "-o", "json")
	if err != nil {
		return paths, err
	}
	var deployment struct {
		Spec struct {
			Template struct {
				Spec struct {
					Containers []struct {
						Env []struct {
							Name  string `json:"name"`
							Value string `json:"value"`
						} `json:"env"`
					} `json:"containers"`
				} `json:"spec"`
			} `json:"template"`
		} `json:"spec"`
	}
	if json.Unmarshal(data, &deployment) != nil {
		return paths, errRecoveryDrill
	}
	values := map[string]string{}
	for _, container := range deployment.Spec.Template.Spec.Containers {
		for _, env := range container.Env {
			values[env.Name] = env.Value
		}
	}
	for i, names := range [][3]string{
		{"LOCO_SECRETS_TRANSIT_MOUNT", "encrypt", "LOCO_SECRETS_TRANSIT_KEY"},
		{"LOCO_SECRETS_TRANSIT_MOUNT", "sign", "LOCO_INGEST_TRANSIT_KEY"},
	} {
		if values[names[0]] == "" || values[names[2]] == "" {
			return paths, errRecoveryDrill
		}
		paths[i] = values[names[0]] + "/" + names[1] + "/" + values[names[2]]
	}
	return paths, nil
}

func provisionRecovery(ctx context.Context, image, keyID, seal string) error {
	input, err := renderFixtureFile("/fixture/recovery.yaml", map[string]any{
		"Namespace": recoveryNamespace, "Pod": recoveryPod, "BaoImage": image, "KeyID": keyID, "Seal": seal,
		"Port": recoveryBaoPort,
	})
	if err != nil {
		return err
	}
	if _, err = command(ctx, "", input, "kubectl", "apply", "-f", "-"); err != nil {
		return err
	}
	_, err = kubectl(
		ctx,
		"-n",
		recoveryNamespace,
		"wait",
		"--for=condition=Ready",
		"pod/"+recoveryPod,
		"--timeout=120s",
	)
	return err
}

func forwardBao(ctx context.Context, client baoClient, namespace, target string) (*exec.Cmd, string, error) {
	remote, err := url.Parse(client.endpoint)
	if err != nil || remote.Port() == "" {
		return nil, "", errRecoveryDrill
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", err
	}
	address := listener.Addr().String()
	if err = listener.Close(); err != nil {
		return nil, "", err
	}
	_, local, err := net.SplitHostPort(address)
	if err != nil {
		return nil, "", err
	}
	forward := exec.CommandContext(
		context.WithoutCancel(ctx),
		"kubectl",
		"-n",
		namespace,
		"port-forward",
		target,
		local+":"+remote.Port(),
		"--address=127.0.0.1",
	)
	forward.Stdout = io.Discard
	forward.Stderr = io.Discard
	if err = forward.Start(); err != nil {
		return nil, "", err
	}
	endpoint := remote.Scheme + "://" + address
	probe := baoClient{client: client.client, endpoint: endpoint}
	// Seal status is available before initialization; authenticated readiness follows init/restore.
	deadline, cancel := context.WithTimeout(ctx, recoveryReadyTimeout)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if _, err = probe.request(deadline, http.MethodGet, "sys/seal-status", nil); err == nil {
			return forward, endpoint, nil
		}
		select {
		case <-deadline.Done():
			return forward, "", errRecoveryDrill
		case <-ticker.C:
		}
	}
}

func waitBao(ctx context.Context, client baoClient) error {
	deadline, cancel := context.WithTimeout(ctx, recoveryReadyTimeout)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if _, err := client.request(deadline, http.MethodGet, "auth/token/lookup-self", nil); err == nil {
			return nil
		}
		select {
		case <-deadline.Done():
			return errRecoveryDrill
		case <-ticker.C:
		}
	}
}

func restartRecovery(ctx context.Context) error {
	count, err := kubectl(
		ctx,
		"-n",
		recoveryNamespace,
		"get",
		"pod",
		recoveryPod,
		"-o",
		"jsonpath={.status.containerStatuses[0].restartCount}",
	)
	if err != nil {
		return err
	}
	restarts, err := strconv.Atoi(string(count))
	if err != nil {
		return errRecoveryDrill
	}
	if _, err = kubectl(
		ctx,
		"-n",
		recoveryNamespace,
		"exec",
		recoveryPod,
		"--",
		"sh",
		"-c",
		"kill -TERM 1",
	); err != nil {
		return err
	}
	if _, err = kubectl(
		ctx,
		"-n",
		recoveryNamespace,
		"wait",
		"--for=jsonpath={.status.containerStatuses[0].restartCount}="+strconv.Itoa(restarts+1),
		"pod/"+recoveryPod,
		"--timeout=120s",
	); err != nil {
		return err
	}

	return nil
}

func restartRestoredBao(ctx context.Context, client baoClient, forward *exec.Cmd) (*exec.Cmd, string, error) {
	deadline, cancel := context.WithTimeout(ctx, recoveryReadyTimeout)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		// A completed restore either accepts the snapshot's root or leaves the auto-seal sealed.
		if _, err := client.request(deadline, http.MethodGet, "auth/token/lookup-self", nil); err == nil {
			break
		}
		data, err := client.request(deadline, http.MethodGet, "sys/seal-status", nil)
		var status struct {
			Sealed bool `json:"sealed"`
		}
		if err == nil && json.Unmarshal(data, &status) == nil && status.Sealed {
			break
		}
		select {
		case <-deadline.Done():
			return forward, client.endpoint, errRecoveryDrill
		case <-ticker.C:
		}
	}

	if err := stopProcess(forward); err != nil {
		return nil, "", err
	}
	// container restart invalidates the old port-forward stream; preserve only raft data.
	if err := restartRecovery(ctx); err != nil {
		return nil, "", err
	}
	client.endpoint = "http://127.0.0.1:" + recoveryBaoPort
	return forwardBao(ctx, client, recoveryNamespace, "pod/"+recoveryPod)
}
