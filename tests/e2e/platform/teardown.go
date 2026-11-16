package platform

import (
	"context"
	"os"
	"strings"
)

const logTailLines = 200

// teardown removes the run: its kind cluster, containers, compose project, images and directory.
func (p *Platform) teardown(ctx context.Context) error {
	if err := p.deleteCluster(ctx); err != nil {
		return err
	}
	if _, err := p.Command(
		ctx,
		"docker",
		"rm",
		"-fv",
		p.ingestContainer(),
		p.clickhouseName(),
		p.realmContainer(),
	); err != nil &&
		!strings.Contains(err.Error(), "No such container") {
		return err
	}
	if _, err := p.compose(ctx, "--profile", "tools", "down", "-v", "--remove-orphans"); err != nil {
		return err
	}
	for _, spec := range []imageSpec{controllerImage, builderImage, obsProxyImage} {
		if _, err := p.Command(ctx, "docker", "image", "rm", newImages(p).tag(spec)); err != nil &&
			!strings.Contains(err.Error(), "No such image") {
			return err
		}
	}
	return os.RemoveAll(p.dir)
}

// dump prints the logs of the host processes, the compose services and the worker's pods, for a
// run whose setup or tests failed.
func (p *Platform) dump(ctx context.Context) {
	group := func(title string, body string) {
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			logf("::group::%s\n%s\n::endgroup::", title, body)
			return
		}
		logf("──── %s\n%s", title, body)
	}
	output := func(name string, args ...string) string {
		out, err := p.Command(ctx, name, args...)
		if err != nil {
			return out + err.Error()
		}
		return out
	}
	for _, name := range []string{apiProcess, replicaProcess, agentProcess, obsProcess} {
		if data, err := os.ReadFile(p.logPath(name)); err == nil {
			group(name+" log", tail(string(data)))
		}
	}
	group("compose services", output("docker", "compose", "-p", p.composeProject, "ps", "-a")+
		output("docker", "compose", "-p", p.composeProject, "logs", "--no-color", "--tail", "200"))
	if _, err := os.Stat(p.kubeconfig); err != nil {
		return
	}
	namespaces := []string{locoNamespace}
	if p.Observability {
		namespaces = append(namespaces, p.values.collectorNS, p.values.nodeCollectorNS)
	}
	for _, namespace := range namespaces {
		group(
			namespace+" pods",
			output("kubectl", "--context", p.kubeContext, "-n", namespace, "get", "pods", "-o", "wide"),
		)
		pods, err := p.Command(ctx, "kubectl", "--context", p.kubeContext, "-n", namespace, "get", "pods", "-o", "name")
		if err != nil {
			group(namespace+" pod listing", err.Error())
			continue
		}
		for pod := range strings.FieldsSeq(pods) {
			group(
				namespace+"/"+pod,
				output("kubectl", "--context", p.kubeContext, "-n", namespace, "logs", pod,
					"--all-containers", "--tail", "200"),
			)
		}
	}
	if p.Observability {
		group("obs-proxy ingest container", output("docker", "logs", "--tail", "200", p.ingestContainer()))
	}
	if p.RegistryToken {
		group("registry token endpoint proxy", output("docker", "logs", "--tail", "200", p.realmContainer()))
	}
}

func tail(text string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) > logTailLines {
		lines = lines[len(lines)-logTailLines:]
	}
	return strings.Join(lines, "\n")
}
