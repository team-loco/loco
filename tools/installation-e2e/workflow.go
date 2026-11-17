package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
)

const (
	phaseUnknown         = "unknown"
	phasePrefix          = "installation-e2e phase: "
	phasePreflight       = "preflight"
	phaseControlPlane    = "control-plane"
	phaseAdminStatus     = "admin-status"
	phasePreLoginAudit   = "pre-login-audit"
	phaseLogin           = "login"
	phaseLoginApprove    = "login-approve"
	phaseLoginApproved   = "login-approved"
	phasePostLoginAudit  = "post-login-audit"
	phaseWorkers         = "workers"
	phaseUpgrade         = "upgrade"
	phaseFreshHome       = "fresh-home"
	phaseWorkloads       = "workloads"
	phaseSourceDeploy    = "source-deploy"
	phaseSourceHTTPS     = "source-https"
	phaseSourceVerify    = "source-verify"
	phaseNetworkPolicy   = "network-policy"
	phaseRecovery        = "recovery"
	phaseTLSMinimum      = "tls-minimum"
	phaseCompletionAudit = "completion-audit"
)

var errOperatorOutputLimit = errors.New("operator output exceeded capture limit")

func operatorWorkflow(ctx context.Context, dir string) ([]byte, error) {
	cmd := exec.CommandContext(
		ctx,
		"kubectl",
		"-n",
		fixtureNamespace,
		"exec",
		"operator",
		"--",
		"installation-e2e",
		"--client",
	)
	cmd.Dir = dir
	// Keep stdout private on failure; stderr carries the operator and kubectl diagnostics.
	output := operatorOutput{phase: phaseUnknown}
	cmd.Stdout = &output
	stderr := operatorOutput{phase: phaseUnknown}
	cmd.Stderr = &stderr
	err := cmd.Run()
	if stderr.phase != phaseUnknown {
		output.phase = stderr.phase
	}
	if stderr.failure != "" {
		output.failure = stderr.failure
	}
	if err != nil && strings.HasPrefix(output.failure, "login failure ") {
		loginDNSDiagnostics(ctx, dir)
	}
	if output.exceeded || stderr.exceeded || len(output.Bytes())+len(stderr.Bytes()) > auditLimit {
		return nil, fmt.Errorf("operator phase %s: %w", output.phase, errOperatorOutputLimit)
	}
	if err != nil {
		if output.failure != "" {
			return nil, fmt.Errorf(
				"operator phase %s, %s: kubectl failed: %w; stderr: %s",
				output.phase,
				output.failure,
				err,
				strings.TrimSpace(string(stderr.Bytes())),
			)
		}
		return nil, fmt.Errorf(
			"operator phase %s: kubectl failed: %w; stderr: %s",
			output.phase,
			err,
			strings.TrimSpace(string(stderr.Bytes())),
		)
	}
	return append(output.Bytes(), stderr.Bytes()...), nil
}

type operatorOutput struct {
	boundedOutput
	lineStart      int
	phase, failure string
}

func (o *operatorOutput) Write(p []byte) (int, error) {
	start := len(o.Bytes())
	n, err := o.boundedOutput.Write(p)
	if err != nil {
		return n, err
	}
	data := o.Bytes()
	for index := start; index < len(data); index++ {
		if data[index] != '\n' {
			continue
		}
		candidate := string(data[o.lineStart:index])
		o.lineStart = index + 1
		if category, ok := strings.CutPrefix(candidate, loginFailurePrefix); ok && knownLoginFailure(category) {
			o.failure = "login failure " + category
		}
		category, isVerification := strings.CutPrefix(candidate, verificationFailurePrefix)
		if isVerification && knownVerificationFailure(category) {
			o.failure = "verification failure " + category
			if _, writeErr := fmt.Fprintln(os.Stderr, verificationFailurePrefix+category); writeErr != nil {
				return n, writeErr
			}
		}
		if slices.Contains([]string{
			phasePrefix + phasePreflight, phasePrefix + phaseControlPlane,
			phasePrefix + phasePreLoginAudit, phasePrefix + phaseLogin,
			phasePrefix + phaseLoginApprove, phasePrefix + phaseLoginApproved,
			phasePrefix + phasePostLoginAudit, phasePrefix + phaseAdminStatus,
			phasePrefix + phaseWorkers, phasePrefix + phaseUpgrade,
			phasePrefix + phaseFreshHome, phasePrefix + phaseWorkloads,
			phasePrefix + phaseSourceDeploy, phasePrefix + phaseSourceHTTPS,
			phasePrefix + phaseSourceVerify, phasePrefix + phaseNetworkPolicy,
			phasePrefix + phaseRecovery, phasePrefix + phaseTLSMinimum, phasePrefix + phaseCompletionAudit,
		}, candidate) {
			o.phase = candidate[len(phasePrefix):]
			if _, writeErr := fmt.Fprintln(os.Stderr, candidate); writeErr != nil {
				return n, writeErr
			}
		}
	}
	return n, nil
}

func operatorPhase(phase string) {
	fmt.Fprintln(os.Stderr, phasePrefix+phase)
}
