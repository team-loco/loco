package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBlockingCI(t *testing.T) {
	t.Parallel()
	const osv = "OSV / Source Baseline / osv-scan"
	const unit = "Tests / unit"
	const ciGate = "CI Gate"
	const failure = "failure"
	jobResults := map[int64]map[string]string{
		1: {osv: failure, unit: ciSuccess, ciGate: failure},
		2: {"OSV": "skipped", unit: ciSuccess, ciGate: ciSuccess},
		3: {osv: ciSuccess, ciGate: ciSuccess},
		4: {osv: failure, unit: failure, ciGate: failure},
		5: {changesJob: failure, osv: "skipped", ciGate: failure},
		6: {changesJob: ciSuccess, osv: ciSuccess, ciGate: ciSuccess},
		7: {changesJob: ciSuccess, osv: ciCanceled, unit: ciSuccess, ciGate: ciCanceled},
	}
	failed := &ciRun{ID: 1, Conclusion: failure}
	skipped := &ciRun{ID: 2, Conclusion: ciSuccess}
	passed := &ciRun{ID: 3, Conclusion: ciSuccess}
	stopped := &ciRun{ID: 7, Conclusion: ciCanceled}
	progress := &ciRun{ID: 8}
	for _, tt := range []struct {
		name    string
		runs    []*ciRun
		blocked string
	}{
		{"skip leaves failure", []*ciRun{failed, skipped}, "ci.yml on a: failure (" + osv + ")"},
		{"later success clears failure", []*ciRun{failed, skipped, nil, passed}, ""},
		{"later run clears only jobs it passed", []*ciRun{{ID: 4, Conclusion: failure}, passed},
			"ci.yml on a: failure (" + unit + ")"},
		{"Changes needs its own rerun", []*ciRun{{ID: 5, Conclusion: failure}, {ID: 6, Conclusion: ciSuccess}},
			"ci.yml on a: failure (CI Gate, Changes)"},
		{"target requires a run", []*ciRun{skipped, nil}, "ci.yml on b: no run"},
		{"target must pass", []*ciRun{skipped, failed}, "ci.yml on b: failure"},
		{"target must finish", []*ciRun{nil, progress}, "ci.yml on b: in progress"},
		{"later success clears cancellation", []*ciRun{stopped, passed}, ""},
		{"skip leaves cancellation", []*ciRun{stopped, skipped}, "ci.yml on a: " + ciCanceled + " (" + osv + ")"},
		{"earlier run must finish", []*ciRun{progress, passed}, "ci.yml on a: in progress"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			commits := make([]commitCI, 0, len(tt.runs))
			for i, run := range tt.runs {
				commits = append(commits, commitCI{sha: string(rune('a' + i)), run: run})
			}
			blocked, err := blockingCI(commits, func(id int64) (map[string]string, error) {
				result, ok := jobResults[id]
				require.True(t, ok)
				return result, nil
			})
			require.NoError(t, err)
			if tt.blocked == "" {
				require.Empty(t, blocked)
			} else {
				require.Equal(t, []string{tt.blocked}, blocked)
			}
		})
	}
}
