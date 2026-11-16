package main

import (
	"encoding/binary"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

const (
	workflowsDir = "../../.github/workflows"
	gateJob      = "gate"
)

type ciJob struct {
	Name    string            `yaml:"name"`
	Uses    string            `yaml:"uses"`
	If      string            `yaml:"if"`
	Needs   []string          `yaml:"needs"`
	Outputs map[string]string `yaml:"outputs"`
}

func readYAML(t *testing.T, path string, into any) {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(data, into), path)
}

func ciJobs(t *testing.T) map[string]ciJob {
	t.Helper()
	var ci struct {
		Jobs map[string]ciJob `yaml:"jobs"`
	}
	readYAML(t, filepath.Join(workflowsDir, ciWorkflowFile), &ci)
	return ci.Jobs
}

// calledWorkflows maps each ci.yml job that calls a workflow in this
// repository to that workflow's file name.
func calledWorkflows(jobs map[string]ciJob) map[string]string {
	called := map[string]string{}
	for id, job := range jobs {
		if file, ok := strings.CutPrefix(job.Uses, "./.github/workflows/"); ok {
			called[id] = file
		}
	}
	return called
}

// TestEveryCalledWorkflowHasAFilter covers the checks the gate waits for, not
// the jobs after it.
func TestEveryCalledWorkflowHasAFilter(t *testing.T) {
	filters, err := loadFilters("../../.github/ci-paths.yml")
	require.NoError(t, err)
	jobs := ciJobs(t)
	called := calledWorkflows(jobs)
	maps.DeleteFunc(called, func(id, _ string) bool { return !slices.Contains(jobs[gateJob].Needs, id) })

	assert.ElementsMatch(t, slices.Collect(maps.Keys(filters)), slices.Collect(maps.Keys(called)))
	for id, file := range called {
		assert.Equal(t, id+".yml", file, "job %s calls the workflow its filter is named after", id)
		assert.Equal(t, "needs.changes.outputs."+id+" == 'true'", jobs[id].If, "job %s", id)
		assert.Equal(t, "${{ steps.select.outputs."+id+" }}", jobs["changes"].Outputs[id], "changes job output %s", id)
	}
}

func TestGateNeedsEveryJob(t *testing.T) {
	jobs := ciJobs(t)
	gate := jobs[gateJob]
	require.Equal(t, "CI Gate", gate.Name, "branch protection requires the check by this name")
	assert.Equal(t, "always()", gate.If)
	var before []string
	for id, job := range jobs {
		if !slices.Contains(job.Needs, gateJob) {
			before = append(before, id)
		}
	}
	assert.ElementsMatch(t, before, append(gate.Needs, gateJob), "every job but those after the gate")
}

// TestCICallsEveryReusableWorkflow keeps a reusable workflow from running
// outside ci.yml.
func TestCICallsEveryReusableWorkflow(t *testing.T) {
	entries, err := os.ReadDir(workflowsDir)
	require.NoError(t, err)
	called := calledWorkflows(ciJobs(t))
	var reusable []string
	for _, entry := range entries {
		var workflow struct {
			On map[string]any `yaml:"on"`
		}
		readYAML(t, filepath.Join(workflowsDir, entry.Name()), &workflow)
		if entry.Name() == ciWorkflowFile {
			continue
		}
		assert.NotContains(t, workflow.On, "pull_request",
			"%s: only ci.yml runs for pull requests, so the gate covers every check", entry.Name())
		_, isReusable := workflow.On["workflow_call"]
		if !isReusable {
			continue
		}
		reusable = append(reusable, entry.Name())
		assert.NotContains(t, workflow.On, "push", "%s: ci.yml runs it on push", entry.Name())
	}
	assert.ElementsMatch(t, reusable, slices.Collect(maps.Values(called)))
}

func TestOperatorToolsUseVerifiedNativeBinaries(t *testing.T) {
	const (
		targetOS          = "linux"
		targetArch        = "amd64"
		operatorTools     = "operator-tools"
		nativeRunnerError = "native Linux runner"
	)
	toolNames := []string{"helm", "kubectl"}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				ID  string `yaml:"id"`
				Run string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	readYAML(t, filepath.Join(workflowsDir, "images.yml"), &workflow)
	var script string
	for _, step := range workflow.Jobs["go"].Steps {
		if step.ID == operatorTools {
			script = step.Run
		}
	}
	require.NotEmpty(t, script, "the operator fixture needs the pinned runner tools")
	verifier, err := os.ReadFile("../platform-images/verify.py")
	require.NoError(t, err)
	for _, tc := range []struct {
		name        string
		hostOS      string
		hostArch    string
		machine     uint16
		interpreter bool
		wantError   string
	}{
		{"native Linux", targetOS, targetArch, 62, false, ""},
		{"Darwin host", "darwin", targetArch, 62, false, nativeRunnerError},
		{"cross compilation", targetOS, "arm64", 62, false, nativeRunnerError},
		{"wrong binary architecture", targetOS, targetArch, 183, false, "does not match amd64"},
		{"dynamic binary", targetOS, targetArch, 62, true, "statically linked"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "bin")
			pinned := filepath.Join(dir, "pinned")
			verifyDir := filepath.Join(dir, "tools", "platform-images")
			for _, path := range []string{bin, pinned, verifyDir} {
				require.NoError(t, os.MkdirAll(path, 0o700))
			}
			require.NoError(t, os.WriteFile(filepath.Join(verifyDir, "verify.py"), verifier, 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(bin, "go"), []byte(`#!/bin/sh
case "$2" in
GOHOSTOS) printf '%s\n' "$OPERATOR_TEST_OS" ;;
GOHOSTARCH) printf '%s\n' "$OPERATOR_TEST_ARCH" ;;
*) exit 1 ;;
esac
`), 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(bin, "mise"), []byte(`#!/bin/sh
[ "$1" = which ] || exit 1
printf '%s/%s\n' "$OPERATOR_TEST_TOOLS" "$2"
`), 0o700))
			elf := make([]byte, 128)
			copy(elf, "\x7fELF\x02\x01")
			binary.LittleEndian.PutUint16(elf[18:], tc.machine)
			binary.LittleEndian.PutUint64(elf[32:], 64)
			binary.LittleEndian.PutUint16(elf[54:], 56)
			binary.LittleEndian.PutUint16(elf[56:], 1)
			if tc.interpreter {
				binary.LittleEndian.PutUint32(elf[64:], 3)
			}
			for _, tool := range toolNames {
				require.NoError(t, os.WriteFile(filepath.Join(pinned, tool), elf, 0o700))
				require.NoError(t, os.WriteFile(filepath.Join(bin, tool), []byte("unverified PATH tool"), 0o700))
			}
			require.NoError(t, os.WriteFile(filepath.Join(pinned, "unrelated.conf"), []byte("excluded"), 0o600))
			cmd := exec.CommandContext(t.Context(), "bash", "-c", script)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
				"ARCH="+targetArch, "OPERATOR_TEST_OS="+tc.hostOS, "OPERATOR_TEST_ARCH="+tc.hostArch,
				"OPERATOR_TEST_TOOLS="+pinned)
			output, err := cmd.CombinedOutput()
			if tc.wantError != "" {
				require.Error(t, err, string(output))
				assert.Contains(t, string(output), tc.wantError)
				return
			}
			require.NoError(t, err, string(output))
			staged, err := os.ReadDir(filepath.Join(bin, operatorTools))
			require.NoError(t, err)
			require.Len(t, staged, 2)
			assert.Equal(t, toolNames, []string{staged[0].Name(), staged[1].Name()})
			for _, file := range staged {
				data, readErr := os.ReadFile(filepath.Join(bin, operatorTools, file.Name()))
				require.NoError(t, readErr)
				assert.Equal(t, elf, data, file.Name())
			}
		})
	}
}
