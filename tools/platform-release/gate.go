package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	ciCanceled  = "cancelled" //nolint:misspell // GitHub Actions conclusion spelling.
	changesJob  = "Changes"
	gateCommand = "gate"
	ciSuccess   = "success"
)

type ciRun struct {
	ID         int64  `json:"databaseId"`
	Conclusion string `json:"conclusion"`
}

type commitCI struct {
	sha string
	run *ciRun
}

func (t *tool) gate(latest, commit string) error {
	if !shaPattern.MatchString(commit) {
		return errors.New("core commit must be a full lowercase Git SHA")
	}
	shas := []string{commit}
	if latest != "" {
		out, err := t.run("git", "rev-list", "--first-parent", "--reverse", "--end-of-options", latest+".."+commit)
		if err != nil {
			return err
		}
		shas = strings.Fields(out)
	}
	// The target always needs its own successful push, even if the range is empty.
	if len(shas) == 0 {
		shas = append(shas, commit)
	}
	commits := make([]commitCI, 0, len(shas))
	for _, sha := range shas {
		var runs []ciRun
		err := t.readCI(&runs, "run", "list", "--workflow", "ci.yml", "--commit", sha,
			"--event", "push", "--limit", "1", "--json", "databaseId,conclusion")
		if err != nil {
			return fmt.Errorf("find CI push for %s: %w", sha, err)
		}
		item := commitCI{sha: sha}
		if len(runs) != 0 {
			item.run = &runs[0]
		}
		commits = append(commits, item)
	}
	blocked, err := blockingCI(commits, t.ciJobs())
	if err != nil {
		return err
	}
	for _, reason := range blocked {
		fmt.Fprintf(os.Stderr, "::error::%s\n", reason)
	}
	if len(blocked) != 0 {
		return errors.New("::error::the push runs above have not succeeded; re-running a failed run clears it")
	}
	return nil
}

func (t *tool) readCI(result any, args ...string) error {
	for attempt := 0; ; attempt++ {
		out, err := t.run(append([]string{"gh"}, args...)...)
		if err == nil {
			return json.Unmarshal([]byte(out), result)
		}
		if attempt == 4 {
			return err
		}
		select {
		case <-t.ctx.Done():
			return t.ctx.Err()
		case <-time.After(time.Second << attempt):
		}
	}
}

func (t *tool) ciJobs() func(int64) (map[string]string, error) {
	cache := map[int64]map[string]string{}
	return func(id int64) (map[string]string, error) {
		if jobs, ok := cache[id]; ok {
			return jobs, nil
		}
		var result struct {
			Jobs []struct {
				Name       string `json:"name"`
				Conclusion string `json:"conclusion"`
			} `json:"jobs"`
		}
		err := t.readCI(&result, "run", "view", strconv.FormatInt(id, 10), "--json", "jobs")
		if err != nil {
			return nil, fmt.Errorf("read jobs for CI run %d: %w", id, err)
		}
		jobs := make(map[string]string, len(result.Jobs))
		for _, job := range result.Jobs {
			jobs[job.Name] = job.Conclusion
		}
		cache[id] = jobs
		return jobs, nil
	}
}

func blockingCI(commits []commitCI, jobs func(int64) (map[string]string, error)) ([]string, error) {
	var blocked []string
	for i, commit := range commits {
		target := i == len(commits)-1
		reason, err := ciBlockReason(commit.run, target, commits[i+1:], jobs)
		if err != nil {
			return nil, err
		}
		if reason != "" {
			blocked = append(blocked, "ci.yml on "+commit.sha+": "+reason)
		}
	}
	return blocked, nil
}

func ciBlockReason(
	run *ciRun,
	target bool,
	later []commitCI,
	jobs func(int64) (map[string]string, error),
) (string, error) {
	if run == nil {
		if target {
			return "no run", nil
		}
		return "", nil
	}
	switch run.Conclusion {
	case ciSuccess:
		return "", nil
	case "":
		return "in progress", nil
	case "failure", ciCanceled:
		if !target {
			pending, err := remainingJobs(run.ID, later, jobs)
			if err != nil || len(pending) == 0 {
				return "", err
			}
			return run.Conclusion + " (" + strings.Join(pending, ", ") + ")", nil
		}
	}
	return run.Conclusion, nil
}

func remainingJobs(id int64, later []commitCI, jobs func(int64) (map[string]string, error)) ([]string, error) {
	results, err := jobs(id)
	if err != nil {
		return nil, err
	}
	failed := map[string]bool{}
	for name, result := range results {
		if result != ciSuccess && result != "skipped" {
			failed[name] = true
		}
	}
	for _, commit := range later {
		if len(failed) == 0 || failed[changesJob] {
			break
		}
		if commit.run == nil {
			continue
		}
		results, err := jobs(commit.run.ID)
		if err != nil {
			return nil, err
		}
		for name, result := range results {
			if result == ciSuccess {
				delete(failed, name)
			}
		}
	}
	pending := make([]string, 0, len(failed))
	for name := range failed {
		pending = append(pending, name)
	}
	sort.Strings(pending)
	return pending, nil
}
