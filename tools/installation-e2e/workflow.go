package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
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
	var output boundedOutput
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	phase := "unknown"
	for line := range bytes.SplitAfterSeq(output.Bytes(), []byte{'\n'}) {
		if !bytes.HasSuffix(line, []byte{'\n'}) {
			continue
		}
		candidate := string(bytes.TrimSuffix(line, []byte{'\n'}))
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
			phase = candidate[len(phasePrefix):]
		}
	}
	if output.exceeded {
		return nil, fmt.Errorf("operator phase %s: %w", phase, errOperatorOutputLimit)
	}
	if err != nil {
		return nil, fmt.Errorf("operator phase %s: kubectl failed: %w", phase, err)
	}
	return output.Bytes(), nil
}

func operatorPhase(phase string) {
	fmt.Fprintln(os.Stderr, phasePrefix+phase)
}
