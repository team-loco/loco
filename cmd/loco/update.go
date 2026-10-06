package loco

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/mod/semver"
)

const (
	updateTimeout       = 5 * time.Minute
	skipVersionCheckKey = "skip-version-check"
)

type updater struct {
	client      *http.Client
	releasesURL string
	goos        string
	goarch      string
}

func newUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "update",
		Short:       "Update the loco CLI to the latest release",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{skipVersionCheckKey: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			u := updater{
				client:      &http.Client{Timeout: updateTimeout},
				releasesURL: releasesURL,
				goos:        runtime.GOOS,
				goarch:      runtime.GOARCH,
			}
			executable, err := os.Executable()
			if err != nil {
				return fmt.Errorf("could not locate the running loco binary: %w", err)
			}
			return u.run(cmd.Context(), cmd.OutOrStdout(), cmd.Root().Version, executable)
		},
	}
}

func (u updater) run(ctx context.Context, w io.Writer, current, executable string) error {
	latest, err := u.latestTag(ctx)
	if err != nil {
		return err
	}
	if semver.IsValid(current) && semver.Compare(current, latest) >= 0 {
		_, err = fmt.Fprintf(w, "loco %s is already the latest version.\n", current)
		return err
	}

	binary, err := u.download(ctx, latest)
	if err != nil {
		return err
	}
	target, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return fmt.Errorf("could not resolve %s: %w", executable, err)
	}
	if err = replaceExecutable(target, binary); err != nil {
		return err
	}

	_, err = fmt.Fprintf(w, "Updated loco %s -> %s\nChangelog: %s/tag/%s\n", current, latest, u.releasesURL, latest)
	return err
}

func (u updater) latestTag(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u.releasesURL+"/latest", nil)
	if err != nil {
		return "", fmt.Errorf("could not build latest release request: %w", err)
	}
	client := *u.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("could not look up the latest loco release: %w", err)
	}
	if err = resp.Body.Close(); err != nil {
		return "", fmt.Errorf("could not look up the latest loco release: %w", err)
	}
	location, err := resp.Location()
	if err != nil {
		return "", fmt.Errorf("could not look up the latest loco release: unexpected %s", resp.Status)
	}
	tag := path.Base(location.Path)
	if !semver.IsValid(tag) {
		return "", fmt.Errorf("latest loco release has an unexpected tag %q", tag)
	}
	return tag, nil
}

func (u updater) download(ctx context.Context, tag string) ([]byte, error) {
	if u.goos != "linux" && u.goos != "darwin" {
		return nil, fmt.Errorf("no loco release for %s", u.goos)
	}
	if u.goarch != "amd64" && u.goarch != "arm64" {
		return nil, fmt.Errorf("no loco release for %s", u.goarch)
	}
	asset := "loco-" + u.goos + "-" + u.goarch
	base, err := url.JoinPath(u.releasesURL, "download", tag)
	if err != nil {
		return nil, fmt.Errorf("could not build release url: %w", err)
	}

	binary, err := u.fetch(ctx, base+"/"+asset)
	if err != nil {
		return nil, err
	}
	checksums, err := u.fetch(ctx, base+"/checksums.txt")
	if err != nil {
		return nil, err
	}
	expected, err := checksumFor(checksums, asset)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(binary)
	if hex.EncodeToString(sum[:]) != expected {
		return nil, fmt.Errorf("checksum verification failed for %s %s", asset, tag)
	}
	return binary, nil
}

func (u updater) fetch(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("could not build request for %s: %w", rawURL, err)
	}
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not download %s: %w", rawURL, err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Debug("could not close response body", "url", rawURL, "error", closeErr)
		}
	}()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("could not download %s: %s", rawURL, resp.Status)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("could not download %s: %w", rawURL, err)
	}
	return data, nil
}

func checksumFor(checksums []byte, asset string) (string, error) {
	scanner := bufio.NewScanner(bytes.NewReader(checksums))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && fields[1] == asset {
			return fields[0], nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("could not read release checksums: %w", err)
	}
	return "", fmt.Errorf("release checksums do not list %s", asset)
}

func replaceExecutable(target string, binary []byte) error {
	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, ".loco-update-*")
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("no permission to write to %s; rerun with sudo or reinstall with %s", dir, installCommand)
		}
		return fmt.Errorf("could not stage the update in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	defer func() {
		if err := os.Remove(tmpPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Debug("could not remove staged update", "path", tmpPath, "error", err)
		}
	}()

	if _, err := tmp.Write(binary); err != nil {
		return errors.Join(fmt.Errorf("could not write the update: %w", err), tmp.Close())
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("could not write the update: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		return fmt.Errorf("could not make the update executable: %w", err)
	}
	if err := os.Rename(tmpPath, target); err != nil {
		return fmt.Errorf("could not replace %s: %w", target, err)
	}
	return nil
}
