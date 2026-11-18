package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type imageCacheEntry struct {
	Fingerprint string `json:"fingerprint"`
	Repository  string `json:"repository"`
	Digest      string `json:"digest"`
}
type localOptions struct{ address, prebuilt, cache string }
type imageResult struct {
	reference string
	cache     imageCacheEntry
	err       error
}

func (t *tool) localRelease(r *registry, options localOptions) (releaseManifest, error) {
	commit, err := t.run("git", "rev-parse", "HEAD")
	if err != nil {
		return releaseManifest{}, err
	}
	if !shaPattern.MatchString(commit) {
		return releaseManifest{}, errors.New("core commit must be a full lowercase Git SHA")
	}
	version := "0.0.0-local.g" + commit[:12]
	manifest := releaseManifest{
		Version:    version,
		CoreCommit: commit,
		Artifacts:  map[string]string{},
		Images:     map[string]string{},
	}
	cached, fingerprints, err := t.imageInputs(options.cache)
	if err != nil {
		return manifest, err
	}

	directory, err := os.MkdirTemp("", "loco-release-")
	if err != nil {
		return manifest, err
	}
	defer os.RemoveAll(directory)
	artifacts, err := t.packagedArtifacts(directory, commit)
	if err != nil {
		return manifest, err
	}
	names := repositories(r.repository)
	for _, artifact := range artifacts {
		digest, publishErr := r.publish(
			t.ctx,
			names[artifact.name],
			version,
			artifact.data,
			artifact.config,
			artifact.mediaType,
			true,
		)
		if publishErr != nil {
			return manifest, publishErr
		}
		manifest.Artifacts[artifact.name] = options.address + "/" + names[artifact.name] + "@" + digest
	}
	auth := base64.StdEncoding.EncodeToString([]byte(r.username + ":" + r.password))
	config := encoded(object{"auths": object{r.host: object{fieldAuth: auth}}})
	if checkErr := os.WriteFile(filepath.Join(directory, "config.json"), config, 0o600); checkErr != nil {
		return manifest, checkErr
	}
	daemon, err := t.run(dockerCommand, "context", "inspect", "--format", "{{.Endpoints.docker.Host}}")
	if err != nil {
		return manifest, err
	}
	results := make([]imageResult, len(images))
	var workers sync.WaitGroup
	for index, name := range images {
		workers.Go(func() {
			results[index] = t.publishImage(
				r,
				options,
				directory,
				daemon,
				version,
				name,
				cached[name],
				fingerprints[name],
			)
		})
	}
	workers.Wait()
	nextCache := map[string]imageCacheEntry{}
	for index, name := range images {
		result := results[index]
		if result.err != nil {
			return manifest, fmt.Errorf("%s: %w", name, result.err)
		}
		manifest.Images[name] = result.reference
		nextCache[name] = result.cache
	}
	files, err := t.bundle(manifest)
	if err != nil {
		return manifest, err
	}
	data, err := archive(files)
	if err != nil {
		return manifest, err
	}
	digest, err := r.publish(
		t.ctx,
		names[fieldPlatform],
		version,
		data,
		object{fieldVersion: version, fieldCoreCommit: commit},
		layerType,
		true,
	)
	if err != nil {
		return manifest, err
	}
	if options.cache != "" {
		if checkErr := os.MkdirAll(filepath.Dir(options.cache), 0o750); checkErr != nil {
			return manifest, checkErr
		}
		if checkErr := os.WriteFile(options.cache, encoded(nextCache), 0o600); checkErr != nil {
			return manifest, checkErr
		}
	}
	manifest.Bundle = options.address + "/" + names[fieldPlatform] + "@" + digest
	fmt.Fprintf(os.Stderr, "published %s:%s@%s\n", options.address+"/"+names[fieldPlatform], version, digest)
	return manifest, nil
}

func (t *tool) publishImage(
	r *registry,
	options localOptions,
	directory, daemon, version, name string,
	previous imageCacheEntry,
	fingerprint string,
) imageResult {
	repository := r.repository + "-" + name
	tag := r.host + "/" + repository + ":" + version
	if options.cache != "" && previous.Fingerprint == fingerprint && previous.Repository == r.host+"/"+repository {
		found, err := r.manifest(t.ctx, repository, previous.Digest, true)
		if err != nil {
			return imageResult{err: err}
		}
		if found != "" {
			return imageResult{reference: options.address + "/" + repository + "@" + found, cache: previous}
		}
	}
	var err error
	if options.prebuilt != "" {
		_, err = t.run(dockerCommand, tagOperation, strings.ReplaceAll(options.prebuilt, "*", name), tag)
	} else {
		_, err = t.run(
			dockerCommand,
			buildOperation,
			"--build-arg",
			"VERSION="+version,
			"-f",
			dockerfiles[name],
			"-t",
			tag,
			".",
		)
	}
	if err != nil {
		return imageResult{err: err}
	}
	if _, checkErr := t.run(
		dockerCommand,
		"--config",
		directory,
		"--host",
		daemon,
		pushOperation,
		tag,
	); checkErr != nil {
		return imageResult{err: checkErr}
	}
	digest, err := r.manifest(t.ctx, repository, version, false)
	if err != nil {
		return imageResult{err: err}
	}
	return imageResult{
		reference: options.address + "/" + repository + "@" + digest,
		cache:     imageCacheEntry{fingerprint, r.host + "/" + repository, digest},
	}
}

func (t *tool) imageInputs(cachePath string) (map[string]imageCacheEntry, map[string]string, error) {
	cached := map[string]imageCacheEntry{}
	fingerprints := map[string]string{}
	if cachePath != "" {
		data, err := os.ReadFile(cachePath)
		if err != nil && !os.IsNotExist(err) {
			return nil, nil, err
		}
		if err == nil {
			if checkErr := json.Unmarshal(data, &cached); checkErr != nil {
				return nil, nil, checkErr
			}
		}
		args := []string{"go", "run", "./tools/image-inputs"}
		for _, name := range images {
			args = append(args, name+"="+dockerfiles[name])
		}
		output, err := t.run(args...)
		if err != nil {
			return nil, nil, err
		}
		if checkErr := json.Unmarshal([]byte(output), &fingerprints); checkErr != nil {
			return nil, nil, checkErr
		}
		for _, name := range images {
			if fingerprints[name] == "" {
				return nil, nil, fmt.Errorf("missing fingerprint for %s", name)
			}
		}
	}
	return cached, fingerprints, nil
}
