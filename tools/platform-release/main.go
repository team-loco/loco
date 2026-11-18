package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
)

const mainCommand = "main"

const localCommand = "local"

type commandOptions struct {
	mode, commit, version, repository, address, push, username, password, cache, prebuilt, base, input, output string
	latest                                                                                                     string
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := execute(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func parseOptions(args []string) (commandOptions, error) {
	options := commandOptions{}
	if len(args) == 0 {
		return options, errors.New(
			"usage: platform-release {artifacts|bundle|main|local|check-versions|render|gate} [flags]",
		)
	}
	options.mode = args[0]
	flags := flag.NewFlagSet(options.mode, flag.ContinueOnError)
	switch options.mode {
	case artifactsName, bundleCommand, mainCommand, localCommand:
		flags.StringVar(&options.repository, "repository", "", "Artifact owner/repository")
		if options.mode != localCommand {
			flags.StringVar(&options.commit, "commit", "", "Source commit")
		}
		if options.mode == bundleCommand {
			flags.StringVar(&options.version, fieldVersion, "", "Stable release version")
		}
		if options.mode == localCommand {
			flags.StringVar(&options.push, pushOperation, "", "Registry address on this machine")
			flags.StringVar(&options.address, "address", "", "Registry address for cluster pulls")
			flags.StringVar(&options.username, "username", "", "Push identity")
			flags.StringVar(&options.password, "password", "", "Push credential")
			flags.StringVar(&options.cache, "cache", "", "Image input cache for local development")
			flags.StringVar(&options.prebuilt, "images", "", "Prebuilt image template, with * for the component")
		}
	case "check-versions":
		flags.StringVar(&options.base, "base", "", "Comparison ref")
	case gateCommand:
		flags.StringVar(&options.latest, "latest", "", "Latest release tag, if any")
		flags.StringVar(&options.commit, "commit", "", "Source commit")
	case "render":
		flags.StringVar(&options.input, "manifest", "", "Release manifest JSON")
		flags.StringVar(&options.output, "output", "", "Bundle output directory")
	default:
		return options, fmt.Errorf("unknown command: %s", options.mode)
	}
	if err := flags.Parse(args[1:]); err != nil {
		return options, err
	}
	if flags.NArg() != 0 {
		return options, fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	return options, nil
}

func execute(ctx context.Context, args []string) error {
	options, err := parseOptions(args)
	if err != nil {
		return err
	}
	root, err := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return fmt.Errorf("find source repository: %w", err)
	}
	t := &tool{root: strings.TrimSpace(string(root)), ctx: ctx}
	result, err := t.execute(options)
	if err != nil {
		return err
	}
	if result != nil {
		fmt.Println(string(encoded(result)))
	}
	return nil
}

func (options commandOptions) registry() (*registry, error) {
	if options.repository == "" {
		return nil, errors.New("--repository is required")
	}
	username, password, host := options.username, options.password, "ghcr.io"
	if options.mode == localCommand {
		if options.push == "" || options.address == "" || username == "" || password == "" {
			return nil, errors.New("--push, --address, --username and --password are required")
		}
		host = options.push
	} else if options.mode != mainCommand {
		username = os.Getenv("GITHUB_ACTOR")
		password = os.Getenv("GH_TOKEN")
		if username == "" || password == "" {
			return nil, errors.New("GITHUB_ACTOR and GH_TOKEN are required")
		}
	}
	return newRegistry(options.repository, username, password, host)
}

func (t *tool) execute(options commandOptions) (any, error) {
	switch options.mode {
	case "check-versions":
		if options.base == "" {
			return nil, errors.New("--base is required")
		}
		stale, err := t.unbumped(options.base)
		if err != nil {
			return nil, err
		}
		if len(stale) != 0 {
			return nil, fmt.Errorf(
				"changed since %s without a version bump: %s",
				options.base,
				strings.Join(stale, ", "),
			)
		}
		return nil, nil
	case gateCommand:
		return nil, t.gate(options.latest, options.commit)
	case "render":
		return nil, t.render(options.input, options.output)
	}
	r, err := options.registry()
	if err != nil {
		return nil, err
	}
	switch options.mode {
	case localCommand:
		return t.localRelease(r, localOptions{options.address, options.prebuilt, options.cache})
	case mainCommand:
		return t.mainRelease(options.commit, r)
	case artifactsName:
		if options.commit == "" {
			return nil, errors.New("--commit is required")
		}
		return t.publishArtifacts(options.commit, r)
	case bundleCommand:
		if options.commit == "" || options.version == "" {
			return nil, errors.New("--commit and --version are required")
		}
		return t.publishBundle(options.version, options.commit, r)
	}
	return nil, fmt.Errorf("unknown command: %s", options.mode)
}
