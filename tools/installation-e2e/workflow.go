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
	phasePrefix                   = "installation-e2e phase: "
	phasePreflight                = "preflight"
	phaseControlPlane             = "control-plane"
	phasePreBootstrapAudit        = "pre-bootstrap-audit"
	phaseBootstrap                = "bootstrap"
	phaseBootstrapAuthorization   = "bootstrap-authorization"
	phaseBootstrapBrowser         = "bootstrap-browser"
	phaseBootstrapBrowserComplete = "bootstrap-browser-complete"
	phaseBootstrapGrant           = "bootstrap-grant"
	phasePostBootstrapAudit       = "post-bootstrap-audit"
	phaseWorkers                  = "workers"
	phaseRepeatApply              = "repeat-apply"
	phaseFreshHome                = "fresh-home"
	phaseWorkloads                = "workloads"
	phaseRecovery                 = "recovery"
	phaseCompletionAudit          = "completion-audit"
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
	// Keep raw child output bounded and private even when kubectl fails.
	output := operatorOutput{phase: "unknown"}
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	if output.exceeded {
		return nil, fmt.Errorf("operator phase %s: %w", output.phase, errOperatorOutputLimit)
	}
	if err != nil {
		if output.failure != "" {
			return nil, fmt.Errorf(
				"operator phase %s, bootstrap failure %s: kubectl failed: %w", output.phase, output.failure, err,
			)
		}
		return nil, fmt.Errorf("operator phase %s: kubectl failed: %w", output.phase, err)
	}
	return output.Bytes(), nil
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
		if category, ok := strings.CutPrefix(candidate, bootstrapFailurePrefix); ok && knownBootstrapFailure(category) {
			o.failure = category
		}
		if slices.Contains([]string{
			phasePrefix + phasePreflight, phasePrefix + phaseControlPlane,
			phasePrefix + phasePreBootstrapAudit, phasePrefix + phaseBootstrap,
			phasePrefix + phaseBootstrapAuthorization, phasePrefix + phaseBootstrapBrowser,
			phasePrefix + phaseBootstrapBrowserComplete, phasePrefix + phaseBootstrapGrant,
			phasePrefix + phasePostBootstrapAudit,
			phasePrefix + phaseWorkers, phasePrefix + phaseRepeatApply,
			phasePrefix + phaseFreshHome, phasePrefix + phaseWorkloads,
			phasePrefix + phaseRecovery, phasePrefix + phaseCompletionAudit,
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
