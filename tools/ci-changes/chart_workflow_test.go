package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChartLintKeepsTheEventBaseAfterMainAdvances(t *testing.T) {
	type step struct {
		ID  string            `yaml:"id"`
		Run string            `yaml:"run"`
		Env map[string]string `yaml:"env"`
	}
	type workflow struct {
		Jobs map[string]struct {
			Steps []step `yaml:"steps"`
		} `yaml:"jobs"`
	}
	var charts, ci workflow
	readYAML(t, filepath.Join(workflowsDir, "charts.yml"), &charts)
	readYAML(t, filepath.Join(workflowsDir, ciWorkflowFile), &ci)
	var lint step
	for _, candidate := range charts.Jobs["checks"].Steps {
		if strings.Contains(candidate.Run, "mise run lint:charts") {
			lint = candidate
		}
	}
	require.NotEmpty(t, lint.Run)
	var eventBase string
	for _, selectStep := range ci.Jobs["changes"].Steps {
		if selectStep.ID == "select" {
			eventBase = selectStep.Env["BASE"]
		}
	}
	require.NotEmpty(t, eventBase)
	assert.Equal(t, eventBase, lint.Env["CHARTS_BASE_SHA"])
	var config struct {
		Remote                string `yaml:"remote"`
		TargetBranch          string `yaml:"target-branch"`
		CheckVersionIncrement bool   `yaml:"check-version-increment"`
	}
	readYAML(t, "../../.ct.yaml", &config)
	require.True(t, config.CheckVersionIncrement)
	comparison := config.Remote + "/" + config.TargetBranch
	const chartPath = "charts/app/Chart.yaml"
	const baseChart = "name: app\nversion: 1.0.0\n"
	for _, tc := range []struct {
		name     string
		advanced bool
		bumped   bool
	}{
		{name: "before merge", bumped: true},
		{name: "after merge", advanced: true, bumped: true},
		{name: "unchanged version", advanced: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			git := func(args ...string) string {
				t.Helper()
				cmd := exec.CommandContext(t.Context(), "git", args...)
				cmd.Dir = dir
				cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
					"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.test",
					"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.test")
				out, err := cmd.CombinedOutput()
				require.NoError(t, err, string(out))
				return strings.TrimSpace(string(out))
			}
			git("init", "--initial-branch=main")
			require.NoError(t, os.MkdirAll(filepath.Join(dir, "charts/app"), 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(dir, chartPath), []byte(baseChart), 0o600))
			git("add", "charts")
			git("commit", "-m", "base")
			base := git("rev-parse", "HEAD")
			if tc.bumped {
				require.NoError(t, os.WriteFile(filepath.Join(dir, chartPath),
					[]byte("name: app\nversion: 1.0.1\n"), 0o600))
			}
			require.NoError(
				t,
				os.WriteFile(filepath.Join(dir, "charts/app/values.yaml"), []byte("enabled: true\n"), 0o600),
			)
			git("add", "charts")
			git("commit", "-m", "change")
			target := base
			if tc.advanced {
				target = git("rev-parse", "HEAD")
			}
			git("update-ref", "refs/remotes/"+comparison, target)
			bin := filepath.Join(dir, "bin")
			require.NoError(t, os.Mkdir(bin, 0o700))
			// ct reads the old Chart.yaml from remote/target-branch even when --since is set.
			require.NoError(t, os.WriteFile(filepath.Join(bin, "mise"), []byte(`#!/bin/sh
[ "$*" = "run lint:charts" ] || exit 1
git show "$CHART_TEST_COMPARISON:charts/app/Chart.yaml" > before.yaml
! cmp -s before.yaml charts/app/Chart.yaml
`), 0o700))
			cmd := exec.CommandContext(t.Context(), "bash", "-e", "-o", "pipefail", "-c", lint.Run)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
				"CHARTS_BASE_SHA="+base, "CHART_TEST_COMPARISON="+comparison)
			out, err := cmd.CombinedOutput()
			if tc.bumped {
				require.NoError(t, err, string(out))
			} else {
				require.Error(t, err, "a chart change without a version increment must still fail")
			}
			before, readErr := os.ReadFile(filepath.Join(dir, "before.yaml"))
			require.NoError(t, readErr)
			assert.Equal(t, baseChart, string(before))
			assert.Equal(t, base, git("rev-parse", comparison))
			assert.Contains(t, git("diff", "--name-only", comparison, "HEAD"), "charts/app/values.yaml")
		})
	}
}
