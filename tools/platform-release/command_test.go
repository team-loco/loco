package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func testTool(t *testing.T) *tool {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	require.NoError(t, os.Mkdir(bin, 0o750))
	executable, err := os.Executable()
	require.NoError(t, err)
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
	for _, name := range []string{"git", "yq", "helm", "mise", dockerCommand, "go", "gh"} {
		script := "#!/bin/sh\nexec " + quote(
			executable,
		) + " -test.run=TestReleaseCommandHelper -- " + name + " \"$@\"\n"
		require.NoError(t, os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755))
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("LOCO_RELEASE_TEST_ROOT", root)
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	write := func(name string, data []byte) {
		path := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, data, 0o644))
	}
	for _, name := range charts {
		write(
			"charts/"+name+"/Chart.yaml",
			encoded(
				object{
					fieldName:      name,
					fieldVersion:   fixtureVersion,
					"dependencies": []object{{fieldName: "gateway-helm", fieldVersion: "1.0.0"}},
				},
			),
		)
	}
	write(
		crdVersions,
		encoded(object{"applications": fixtureVersion, buildsName: fixtureVersion, gatewayName: fixtureVersion}),
	)
	write(
		certManager,
		encoded(
			object{"chart": certManagerName, "repository": "https://chart.example.invalid", fieldVersion: "v1.0.0"},
		),
	)
	for _, entry := range crdSources[:2] {
		write(
			entry.path,
			encoded(
				object{
					fieldAPIVersion: "apiextensions.k8s.io/v1",
					fieldKind:       "CustomResourceDefinition",
					fieldMetadata:   object{fieldName: entry.name},
				},
			),
		)
	}
	write(
		"manifests/namespaces/loco-system.yaml",
		encoded(object{fieldAPIVersion: "v1", fieldKind: "Namespace", fieldMetadata: object{fieldName: "loco-system"}}),
	)
	write(
		"charts/loco-worker/charts/gateway-helm-1.0.0.tgz",
		mustArchive(t, releaseFiles{"gateway/crds/crds/gateway.yaml": []byte("kind: CustomResourceDefinition")}),
	)
	write("chart.tgz", mustArchive(t, releaseFiles{"Chart.yaml": []byte("name: fixture\nversion: 0.1.0")}))
	fingerprints := map[string]string{}
	for _, name := range images {
		fingerprints[name] = name
	}
	write("fingerprints.json", encoded(fingerprints))
	return &tool{root: root, ctx: context.Background()}
}

func TestReleaseCommandHelper(_ *testing.T) {
	root := os.Getenv("LOCO_RELEASE_TEST_ROOT")
	if root == "" {
		return
	}
	separator := 0
	for index, arg := range os.Args {
		if arg == "--" {
			separator = index
			break
		}
	}
	args := os.Args[separator+1:]
	if checkErr := releaseCommandHelper(root, args); checkErr != nil {
		fmt.Fprintln(os.Stderr, checkErr)
		os.Exit(1)
	}
	os.Exit(0)
}

func releaseCommandHelper(root string, args []string) error {
	log, err := os.OpenFile(filepath.Join(root, "calls.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	_, err = log.Write(append(encoded(args), '\n'))
	closeErr := log.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	fixture := commandFixture{root: root, args: args}
	switch args[0] {
	case "git":
		return fixture.git()
	case "yq":
		return fixture.yq()
	case "go":
		data, err := fixture.read("fingerprints.json")
		if err != nil {
			return err
		}
		fmt.Print(string(data))
		return nil
	case "helm":
		return fixture.helm()
	case dockerCommand:
		return fixture.docker()
	case "mise", "gh":
		return nil
	default:
		return fmt.Errorf("unexpected fixture command %s", args[0])
	}
}

type commandFixture struct {
	root string
	args []string
}

func (fixture commandFixture) read(name string) ([]byte, error) {
	return os.ReadFile(filepath.Join(fixture.root, name))
}
func (commandFixture) write(name string, data []byte) error {
	return os.WriteFile(name, data, 0o600)
}
func (fixture commandFixture) argument(flag string) string {
	for index, value := range fixture.args {
		if value == flag && index+1 < len(fixture.args) {
			return fixture.args[index+1]
		}
	}
	return ""
}

func (fixture commandFixture) git() error {
	switch fixture.args[1] {
	case "rev-parse":
		fmt.Print(strings.Repeat("b", 40))
	case "ls-tree":
		fmt.Print(fixture.args[len(fixture.args)-1])
	case "show":
		_, path, _ := strings.Cut(fixture.args[2], ":")
		data, err := fixture.read(filepath.Join("base", path))
		if os.IsNotExist(err) {
			data, err = fixture.read(path)
		}
		if err != nil {
			return err
		}
		fmt.Print(string(data))
	case "rev-list":
		fmt.Print(strings.Repeat("b", 40))
	}

	return nil
}

func (fixture commandFixture) yq() error {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return err
	}
	var value any
	if checkErr := json.Unmarshal(data, &value); checkErr != nil {
		return checkErr
	}
	expression := fixture.args[2]
	if expression != "." {
		document, ok := value.(map[string]any)
		if !ok {
			return errors.New("expected document")
		}
		if strings.HasPrefix(expression, ".dependencies") {
			dependencies, ok := document["dependencies"].([]any)
			if !ok {
				return errors.New("missing dependencies")
			}
			value = nil
			for _, dependency := range dependencies {
				entry, ok := dependency.(map[string]any)
				if !ok {
					return errors.New("invalid fixture dependency")
				}
				if entry[fieldName] == "gateway-helm" {
					value = entry
					if strings.HasSuffix(expression, ".version") {
						value = entry[fieldVersion]
					}
					break
				}
			}
		} else {
			value = document[strings.TrimPrefix(expression, ".")]
		}
	}
	fmt.Print(string(encoded(value)))

	return nil
}

func (fixture commandFixture) helm() error {
	name := "cert-manager-v1.0.0"
	if fixture.args[1] == "package" {
		name = filepath.Base(fixture.args[2]) + "-0.1.0"
	}
	data, err := fixture.read("chart.tgz")
	if err != nil {
		return err
	}
	return fixture.write(filepath.Join(fixture.argument("--destination"), name+".tgz"), data)
}

func (fixture commandFixture) docker() error {
	if len(fixture.args) > 2 && fixture.args[1] == "context" {
		fmt.Print("unix:///fixture.sock")
		return nil
	}
	operation := buildOperation
	tag := fixture.argument("-t")
	if tag == "" {
		operation = pushOperation
		tag = fixture.args[len(fixture.args)-1]
	}
	if fixture.args[1] == tagOperation {
		operation = tagOperation
		tag = fixture.args[len(fixture.args)-1]
	}
	name := ""
	for _, candidate := range images {
		if strings.Contains(tag, "-"+candidate+":") {
			name = candidate
			break
		}
	}
	if operation == pushOperation {
		directory := fixture.argument("--config")
		info, err := os.Stat(filepath.Join(directory, "config.json"))
		if err != nil {
			return err
		}
		if info.Mode().Perm() != 0o600 {
			return errors.New("push credential mode is not 0600")
		}
	}
	failure, readErr := fixture.read("failure")
	if readErr != nil && !os.IsNotExist(readErr) {
		return readErr
	}
	if string(failure) == operation+"-"+name {
		return fmt.Errorf("requested %s failure", operation)
	}
	if checkErr := fixture.write(filepath.Join(fixture.root, operation+"-"+name), nil); checkErr != nil {
		return checkErr
	}
	if _, checkErr := os.Stat(filepath.Join(fixture.root, "overlap")); checkErr == nil && operation != tagOperation {
		deadline := time.Now().Add(5 * time.Second)
		for {
			matches, checkErr := filepath.Glob(filepath.Join(fixture.root, operation+"-*"))
			if checkErr != nil {
				return checkErr
			}
			if len(matches) == len(images) {
				break
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("%s operations did not overlap", operation)
			}
			time.Sleep(time.Millisecond)
		}
	}
	return nil
}

func commandCalls(t *testing.T, root string) [][]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "calls.jsonl"))
	require.NoError(t, err)
	calls := make([][]string, 0, strings.Count(string(data), "\n"))
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		var args []string
		require.NoError(t, json.Unmarshal([]byte(line), &args))
		calls = append(calls, args)
	}
	return calls
}

const (
	fixtureVersion = "0.1.0"
	fixtureBearer  = "Bearer"
	fixtureAddress = "registry.localhost:15000"
)
