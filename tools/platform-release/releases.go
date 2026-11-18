package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

const mainCommits = 50

func (t *tool) publishArtifacts(commit string, r *registry) (map[string]string, error) {
	if checkErr := t.validateCommit(commit); checkErr != nil {
		return nil, checkErr
	}
	directory, err := os.MkdirTemp("", "loco-release-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(directory)
	artifacts, err := t.packagedArtifacts(directory, commit)
	if err != nil {
		return nil, err
	}
	names := repositories(r.repository)
	result := map[string]string{}
	public := r.public()
	for _, artifact := range artifacts {
		ref, err := r.ensure(
			t.ctx,
			names[artifact.name],
			artifact.version,
			artifact.data,
			artifact.config,
			artifact.mediaType,
		)
		if err != nil {
			return nil, err
		}
		found, err := public.manifest(t.ctx, names[artifact.name], ref, false)
		if err != nil {
			return nil, err
		}
		if found != ref {
			return nil, fmt.Errorf("artifact must permit anonymous pulls: %s", artifact.name)
		}
		result[artifact.name] = r.host + "/" + names[artifact.name] + ":" + artifact.version + "@" + ref
	}
	return result, nil
}

func (t *tool) published(commit string, r *registry, ref string) (releaseManifest, error) {
	manifest := releaseManifest{CoreCommit: commit, Artifacts: map[string]string{}, Images: map[string]string{}}
	names := repositories(r.repository)
	public := r.public()
	versions, err := t.versions(ref)
	if err != nil {
		return manifest, err
	}
	for name, pinned := range versions {
		digest, err := public.manifest(t.ctx, names[name], pinned, true)
		if err != nil {
			return manifest, err
		}
		if digest == "" {
			return manifest, fmt.Errorf("%s %s is not published", name, pinned)
		}
		manifest.Artifacts[name] = r.host + "/" + names[name] + "@" + digest
	}
	for _, name := range images {
		repository := r.repository + "-" + name
		digest, err := r.manifest(t.ctx, repository, "sha-"+commit, false)
		if err != nil {
			return manifest, err
		}
		if checkErr := r.requireImagePlatforms(t.ctx, repository, digest); checkErr != nil {
			return manifest, checkErr
		}
		reference := r.host + "/" + repository + "@" + digest
		manifest.Images[name] = reference
		if _, checkErr := t.run(
			"gh",
			"attestation",
			"verify",
			"oci://"+reference,
			"--repo",
			r.repository,
			"--signer-workflow",
			r.repository+"/.github/workflows/build-push.yml",
			"--source-ref",
			"refs/heads/main",
			"--source-digest",
			commit,
			"--deny-self-hosted-runners",
		); checkErr != nil {
			return manifest, checkErr
		}
		found, err := public.manifest(t.ctx, repository, digest, false)
		if err != nil {
			return manifest, err
		}
		if found != digest {
			return manifest, errors.New("platform images must permit anonymous pulls")
		}
	}
	return manifest, nil
}

func (t *tool) publishBundle(version, commit string, r *registry) (releaseManifest, error) {
	if checkErr := validateVersion(version); checkErr != nil {
		return releaseManifest{}, checkErr
	}
	if strings.Contains(version, "-") {
		return releaseManifest{}, errors.New("platform bundles require a stable X.Y.Z release")
	}
	if checkErr := t.validateCommit(commit); checkErr != nil {
		return releaseManifest{}, checkErr
	}
	repository := repositories(r.repository)[fieldPlatform]
	if checkErr := r.assertAbsent(t.ctx, repository, version); checkErr != nil {
		return releaseManifest{}, checkErr
	}
	manifest, err := t.published(commit, r, "")
	if err != nil {
		return manifest, err
	}
	manifest.Version = version
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
		repository,
		version,
		data,
		object{fieldVersion: version, fieldCoreCommit: commit},
		layerType,
		false,
	)
	if err != nil {
		return manifest, err
	}
	found, err := r.public().manifest(t.ctx, repository, digest, false)
	if err != nil {
		return manifest, err
	}
	if found != digest {
		return manifest, errors.New("published platform bundle must permit anonymous pulls")
	}
	fmt.Fprintf(os.Stderr, "published %s/%s:%s@%s\n", r.host, repository, version, digest)
	return manifest, nil
}

func (t *tool) newestPublishedCommit(r *registry) (string, error) {
	commits, err := t.run("git", "rev-list", "--first-parent", "-n", strconv.Itoa(mainCommits), "origin/main")
	if err != nil {
		return "", err
	}
	for commit := range strings.FieldsSeq(commits) {
		complete := true
		for _, name := range images {
			found, err := r.manifest(t.ctx, r.repository+"-"+name, "sha-"+commit, true)
			if err != nil {
				return "", err
			}
			if found == "" {
				complete = false
				break
			}
		}
		if complete {
			return commit, nil
		}
	}
	return "", fmt.Errorf("none of the last %d commits of origin/main has published images", mainCommits)
}

func (t *tool) mainRelease(commit string, r *registry) (releaseManifest, error) {
	var err error
	if commit == "" {
		commit, err = t.newestPublishedCommit(r)
		if err != nil {
			return releaseManifest{}, err
		}
	}
	if !shaPattern.MatchString(commit) {
		return releaseManifest{}, errors.New("core commit must be a full lowercase Git SHA")
	}
	if _, checkErr := t.run("git", "merge-base", "--is-ancestor", commit, "origin/main"); checkErr != nil {
		return releaseManifest{}, checkErr
	}
	manifest, err := t.published(commit, r, commit)
	manifest.Version = "0.0.0-main.g" + commit[:12]
	return manifest, err
}
