// Command ci-changes decides which workflows .github/workflows/ci.yml calls for
// a pull request or push, from the changed files and .github/ci-paths.yml, and
// writes one `<workflow>=true|false` line per workflow to $GITHUB_OUTPUT.
package main

import (
	"bytes"
	"context"
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
	flag.Parse()
	if err := run(context.Background(), *config, *event, *base, *head, os.Getenv("GITHUB_OUTPUT")); err != nil {
		slog.Error("select workflows", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, configPath, event, base, head, outputPath string) error {
	filters, err := loadFilters(configPath)
	if err != nil {
		return err
	}
	files, err := changedFiles(ctx, ".", event, base, head)
	if err != nil {
		return err
	}
	output, err := os.OpenFile(outputPath, os.O_APPEND|os.O_WRONLY, 0) //nolint:gosec // the runner names this file
	if err != nil {
		return fmt.Errorf("open $GITHUB_OUTPUT: %w", err)
	}
	defer output.Close()
	return writeSelection(io.MultiWriter(output, os.Stdout), selectWorkflows(filters, event, files))
}

// filter is one event's ordered pattern list; a later pattern overrides an
// earlier one for the paths both match.
type filter []pattern

type pattern struct {
	negated bool
	re      *regexp.Regexp
}

// filters maps a workflow to its filter per event.
type filters map[string]map[string]filter

func loadFilters(path string) (filters, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw map[string]map[string][]string
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	result := filters{}
	for workflow, events := range raw {
		result[workflow] = map[string]filter{}
		for event, patterns := range events {
			if event != "pull_request" && event != "push" {
				return nil, fmt.Errorf("%s: %s: unknown event %q", path, workflow, event)
			}
			f, err := compileFilter(patterns)
			if err != nil {
				return nil, fmt.Errorf("%s: %s: %s: %w", path, workflow, event, err)
			}
			result[workflow][event] = f
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

// selectWorkflows reports, for every workflow, whether a changed file matches
// its filter for the event. Nil files select every workflow with a filter for
// the event.
func selectWorkflows(all filters, event string, files []string) map[string]bool {
	selected := map[string]bool{}
	for workflow, events := range all {
		f, ok := events[event]
		selected[workflow] = ok && (files == nil || slices.ContainsFunc(files, f.matches))
	}
	return selected
}

func writeSelection(w io.Writer, selected map[string]bool) error {
	var b strings.Builder
	for _, workflow := range slices.Sorted(maps.Keys(selected)) {
		fmt.Fprintf(&b, "%s=%t\n", workflow, selected[workflow])
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
