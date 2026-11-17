// Package devicelogin approves CLI device logins through a development identity provider.
package devicelogin

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"connectrpc.com/connect/v2"

	authv1 "github.com/team-loco/loco/gen/go/loco/auth/v1"
	"github.com/team-loco/loco/internal/rpc"
)

var (
	errLogin            = errors.New("locoadm login failed")
	errLoginCodeMissing = errors.New("locoadm login printed no device code")
	errLoginDrain       = errors.New("drain locoadm login output failed")
	errLoginOutputLimit = errors.New("locoadm login output exceeded scanner limit")
	errLoginStop        = errors.New("stop locoadm login failed")
	errProviderToken    = errors.New("development IdP did not issue an ID token")
	errDeviceApproval   = errors.New("approve the device code with the IdP token failed")
)

var deviceCodeLine = regexp.MustCompile(`^Open \S+ and enter the code (\S+)$`)

// Run starts cmd, reads its device code and waits for approval and CLI completion.
// transcript receives stdout for credential auditing; cmd.Stderr belongs to the caller.
func Run(ctx context.Context, cmd *exec.Cmd, transcript io.Writer, approve func(context.Context, string) error) error {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if checkErr := cmd.Start(); checkErr != nil {
		return fmt.Errorf("start locoadm login: %w", checkErr)
	}
	output := io.TeeReader(stdout, transcript)
	scanner := bufio.NewScanner(output)
	code := ""
	for code == "" && scanner.Scan() {
		if match := deviceCodeLine.FindStringSubmatch(scanner.Text()); match != nil {
			code = match[1]
		}
	}
	if scanner.Err() != nil {
		return errors.Join(errLoginOutputLimit, stopLogin(cmd, output))
	}
	if code == "" {
		return errors.Join(errLoginCodeMissing, cmd.Wait())
	}
	if checkErr := approve(ctx, code); checkErr != nil {
		return errors.Join(checkErr, stopLogin(cmd, output))
	}
	_, copyErr := io.Copy(io.Discard, output)
	if waitErr := cmd.Wait(); copyErr != nil || waitErr != nil {
		return errLogin
	}
	return nil
}

func stopLogin(cmd *exec.Cmd, output io.Reader) error {
	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return errLoginStop
	}
	// Capture queued stdout before Wait closes the pipe.
	_, drainErr := io.Copy(io.Discard, output)
	stopErr := cmd.Wait()
	var exit *exec.ExitError
	if errors.As(stopErr, &exit) && exit.ExitCode() == -1 {
		stopErr = nil
	}
	if drainErr != nil {
		return errors.Join(errLoginDrain, stopErr)
	}
	return stopErr
}

// Identity holds the password-grant credentials of a development identity provider.
type Identity struct {
	Issuer   string
	ClientID string
	Email    string
	Password string
}

// Token obtains an ID token without exposing provider response bodies in errors.
func (identity Identity) Token(ctx context.Context) (string, error) {
	form := url.Values{
		"grant_type": {"password"},
		"client_id":  {identity.ClientID},
		"username":   {identity.Email},
		"password":   {identity.Password},
		"scope":      {"openid email profile"},
	}
	request, err := http.NewRequestWithContext(
		ctx, http.MethodPost, identity.Issuer+"/token", strings.NewReader(form.Encode()),
	)
	if err != nil {
		return "", errProviderToken
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := (&http.Client{Timeout: time.Minute}).Do(request)
	if err != nil {
		return "", errProviderToken
	}
	defer response.Body.Close()
	var tokens struct {
		IDToken string `json:"id_token"`
	}
	if response.StatusCode != http.StatusOK ||
		json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&tokens) != nil ||
		tokens.IDToken == "" {
		return "", errProviderToken
	}
	return tokens.IDToken, nil
}

// Approve calls the same API as the dashboard's device-approval page.
func Approve(ctx context.Context, host, idToken, code string) error {
	_, err := rpc.ForHost(host, idToken).Auth.ApproveDeviceLogin(ctx, &authv1.ApproveDeviceLoginRequest{UserCode: code})
	if err != nil {
		return fmt.Errorf("%w (RPC %s)", errDeviceApproval, connect.CodeOf(err))
	}
	return nil
}
