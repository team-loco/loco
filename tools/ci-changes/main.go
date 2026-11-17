// Command ci-changes decides which workflows .github/workflows/ci.yml calls for
// a pull request or push, from the changed files and .github/ci-paths.yml, and
// writes one `<workflow>=true|false` line per workflow to $GITHUB_OUTPUT. A
// workflow whose pass marker for the checked-out inputs exists in a cache the
// run can read is reported false; `markers` maps each workflow that runs and
// may reuse a pass to the cache key its pass writes. `go-modules` lists the
// directories of the Go modules whose lint and tests the changes need.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

const zeroSHA = "0000000000000000000000000000000000000000"

func main() {
	config := flag.String("config", ".github/ci-paths.yml", "path filters for each called workflow")
	event := flag.String("event", "", "pull_request or push")
	base := flag.String("base", "", "pull request base commit, or the commit before a push")
	head := flag.String("head", "", "pull request head commit, or the pushed commit")
	markerRefs := flag.String("marker-refs", "",
		"comma-separated refs whose caches the run can read; empty looks up no pass markers")
	duplicate := flag.Bool("duplicate", false, "reuse deterministic checks of an identical successful PR run")
	flag.Parse()
	var lookup markerLookup
	if *duplicate && *event == "pull_request" {
		lookup = func(context.Context, string) (bool, error) { return true, nil }
	} else if *markerRefs != "" {
		lookup = cacheLookup(strings.Split(*markerRefs, ","))
	}
	if err := run(context.Background(), *config, *event, *base, *head, os.Getenv("GITHUB_OUTPUT"), lookup); err != nil {
		slog.Error("select workflows", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, configPath, event, base, head, outputPath string, lookup markerLookup) error {
	filters, err := loadFilters(configPath)
	if err != nil {
		return err
	}
	files, err := changedFiles(ctx, ".", event, base, head)
	if err != nil {
		return err
	}
	own := selectOwn(filters, event, files)
	modules, err := loadModules(ctx, ".")
	if err != nil {
		return err
	}
	keys, err := markerKeys(ctx, ".", filters, own)
	if err != nil {
		return err
	}
	if lookup != nil {
		for _, workflow := range passed(ctx, lookup, keys) {
			slog.Info("reusing a pass for the same inputs", "workflow", workflow, "marker", keys[workflow])
			own[workflow] = false
			delete(keys, workflow)
		}
	}
	output, err := os.OpenFile(outputPath, os.O_APPEND|os.O_WRONLY, 0) //nolint:gosec // the runner names this file
	if err != nil {
		return fmt.Errorf("open $GITHUB_OUTPUT: %w", err)
	}
	defer output.Close()
	return writeSelection(io.MultiWriter(output, os.Stdout), withUsers(filters, own), keys, modules.affected(files), affectedShards(filters["e2e"].events[event], files))
}

// filter is one event's ordered pattern list; a later pattern overrides an
// earlier one for the paths both match.
type filter []pattern

type pattern struct {
	negated bool
	re      *regexp.Regexp
}

// filters maps each workflow to its paths.
type filters map[string]workflowFilter

type workflowFilter struct {
	// events holds the filter for each event the workflow runs on.
	events map[string]filter
	// usedBy lists the workflows that need this one's output, such as the
	// images it builds; it runs whenever any of them does.
	usedBy []string
	// reuse reports whether a pass for the same inputs stands in for a run.
	reuse bool
}

const usedByKey = "for"

type rawWorkflow struct {
	PullRequest []string `yaml:"pull_request"`
	Push        []string `yaml:"push"`
	For         []string `yaml:"for"`
	Reuse       *bool    `yaml:"reuse"`
}

func loadFilters(path string) (filters, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw map[string]rawWorkflow
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	result := filters{}
	for workflow, entry := range raw {
		w := workflowFilter{events: map[string]filter{}, usedBy: entry.For, reuse: len(entry.For) == 0}
		if entry.Reuse != nil {
			w.reuse = *entry.Reuse && w.reuse
		}
		for event, patterns := range map[string][]string{"pull_request": entry.PullRequest, "push": entry.Push} {
			if patterns == nil {
				continue
			}
			f, err := compileFilter(patterns)
			if err != nil {
				return nil, fmt.Errorf("%s: %s: %s: %w", path, workflow, event, err)
			}
			w.events[event] = f
		}
		result[workflow] = w
	}
	for workflow, w := range result {
		for _, user := range w.usedBy {
			if u, ok := result[user]; !ok || len(u.usedBy) > 0 {
				return nil, fmt.Errorf("%s: %s: %s names %q, which is not a workflow with only its own paths",
					path, workflow, usedByKey, user)
			}
		}
	}
	return result, nil
}

// changedFiles lists the paths a pull request or push changes. A nil result
// means every path counts as changed: a push that created the branch has no
// commit before it to diff against.
func changedFiles(ctx context.Context, dir, event, base, head string) ([]string, error) {
	switch event {
	case "pull_request":
		mergeBase, err := git(ctx, dir, "merge-base", base, head)
		if err != nil {
			return nil, err
		}
		base = strings.TrimSpace(string(mergeBase))
	case "push":
		if base == "" || base == zeroSHA {
			return nil, nil
		}
	default:
		return nil, fmt.Errorf("unsupported event %q", event)
	}
	diff, err := git(ctx, dir, "diff", "--no-renames", "--name-only", "-z", base, head)
	if err != nil {
		return nil, err
	}
	if len(diff) == 0 {
		return []string{}, nil
	}
	return strings.Split(strings.TrimSuffix(string(diff), "\x00"), "\x00"), nil
}

func git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, stderr.String())
	}
	return out, nil
}

// selectOwn reports, for every workflow, whether a changed file matches its
// filter for the event. Nil files select every workflow with a filter for the
// event.
func selectOwn(all filters, event string, files []string) map[string]bool {
	selected := map[string]bool{}
	for workflow, w := range all {
		f, ok := w.events[event]
		selected[workflow] = ok && (files == nil || slices.ContainsFunc(files, f.matches))
	}
	return selected
}

// withUsers also selects each workflow a selected workflow uses.
func withUsers(all filters, own map[string]bool) map[string]bool {
	selected := maps.Clone(own)
	for workflow, w := range all {
		selected[workflow] = selected[workflow] || slices.ContainsFunc(w.usedBy, func(user string) bool {
			return own[user]
		})
	}
	return selected
}

// A test in a shard package is compiled only into that package's test binary.
// All other E2E inputs, including shared code and fixtures, exercise every shard.
func affectedShards(inputs filter, files []string) []string {
	all := []string{"apps", "builds", "telemetry"}
	if files == nil {
		return all
	}
	var selected []string
	for _, file := range files {
		if !inputs.matches(file) {
			continue
		}
		dir, name, ok := strings.Cut(strings.TrimPrefix(file, "tests/e2e/"), "/")
		if !strings.HasPrefix(file, "tests/e2e/") || !ok || !slices.Contains(all, dir) ||
			strings.Contains(name, "/") || !strings.HasSuffix(name, "_test.go") {
			return all
		}
		selected = append(selected, dir)
	}
	slices.Sort(selected)
	return slices.Compact(selected)
}

func writeSelection(w io.Writer, selected map[string]bool, markers map[string]string, goModules, shards []string) error {
	var b strings.Builder
	for _, workflow := range slices.Sorted(maps.Keys(selected)) {
		fmt.Fprintf(&b, "%s=%t\n", workflow, selected[workflow])
	}
	for _, output := range []struct {
		name  string
		value any
	}{{"markers", markers}, {"go-modules", goModules}, {"e2e-shards", shards}} {
		encoded, err := json.Marshal(output.value)
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "%s=%s\n", output.name, encoded)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func (f filter) matches(path string) bool {
	included := false
	for _, p := range f {
		if p.re.MatchString(path) {
			included = !p.negated
		}
	}
	return included
}

func compileFilter(patterns []string) (filter, error) {
	if len(patterns) == 0 {
		return nil, errors.New("empty pattern list")
	}
	f := make(filter, 0, len(patterns))
	for _, raw := range patterns {
		body, negated := strings.CutPrefix(raw, "!")
		expr, err := translate(body)
		if err != nil {
			return nil, fmt.Errorf("pattern %q: %w", raw, err)
		}
		re, err := regexp.Compile("^" + expr + "$")
		if err != nil {
			return nil, fmt.Errorf("pattern %q: %w", raw, err)
		}
		f = append(f, pattern{negated: negated, re: re})
	}
	return f, nil
}

// translate turns a workflow paths pattern into a regular expression. The
// characters GitHub documents but no filter here uses ([], +) are rejected
// rather than guessed at.
func translate(glob string) (string, error) {
	var b strings.Builder
	literal := false
	for i := 0; i < len(glob); i++ {
		rest := glob[i:]
		switch {
		case strings.HasPrefix(rest, "**/"):
			b.WriteString("(?:.*/)?")
			i += 2
			literal = false
		case strings.HasPrefix(rest, "**"):
			b.WriteString(".*")
			i++
			literal = false
		case rest[0] == '*':
			b.WriteString("[^/]*")
			literal = false
		case rest[0] == '?':
			if !literal {
				return "", errors.New("? must follow a literal character")
			}
			b.WriteString("?")
			literal = false
		case rest[0] == '{':
			end := strings.IndexByte(rest, '}')
			if end < 0 {
				return "", errors.New("unclosed {")
			}
			var alternatives []string
			for alternative := range strings.SplitSeq(rest[1:end], ",") {
				expr, err := translate(alternative)
				if err != nil {
					return "", err
				}
				alternatives = append(alternatives, expr)
			}
			b.WriteString("(?:" + strings.Join(alternatives, "|") + ")")
			i += end
			literal = false
		case strings.ContainsRune("[]+}", rune(rest[0])):
			return "", fmt.Errorf("unsupported character %q", rest[0])
		default:
			b.WriteString(regexp.QuoteMeta(rest[:1]))
			literal = true
		}
	}
	return b.String(), nil
}
