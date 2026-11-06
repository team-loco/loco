package main

import (
	"maps"
	"os"
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
	ciWorkflow   = "ci.yml"
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
	readYAML(t, filepath.Join(workflowsDir, ciWorkflow), &ci)
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

func TestEveryCalledWorkflowHasAFilter(t *testing.T) {
	filters, err := loadFilters("../../.github/ci-paths.yml")
	require.NoError(t, err)
	jobs := ciJobs(t)
	called := calledWorkflows(jobs)

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
	assert.ElementsMatch(t, slices.Collect(maps.Keys(jobs)), append(gate.Needs, gateJob))
}

// TestCICallsEveryReusableWorkflow keeps a reusable workflow from being added
// outside the gate. build-push.yml is the one exception: build.yml calls it to
// publish images from main.
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
		if entry.Name() == ciWorkflow {
			continue
		}
		assert.NotContains(t, workflow.On, "pull_request",
			"%s: only ci.yml runs for pull requests, so the gate covers every check", entry.Name())
		_, isReusable := workflow.On["workflow_call"]
		if !isReusable || entry.Name() == "build-push.yml" {
			continue
		}
		reusable = append(reusable, entry.Name())
		assert.NotContains(t, workflow.On, "push", "%s: ci.yml runs it on push", entry.Name())
	}
	assert.ElementsMatch(t, reusable, slices.Collect(maps.Values(called)))
}
