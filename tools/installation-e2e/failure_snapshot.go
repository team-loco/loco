package main

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"maps"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"
)

const failureSnapshotTimeout = 30 * time.Second
const snapshotFailed = "Failed"
const snapshotTrue = "True"
const snapshotReady = "Ready"

var snapshotCPU = regexp.MustCompile(`^[0-9]{1,12}(\.[0-9]{1,9})?m?$`)
var snapshotMemory = regexp.MustCompile(`^[0-9]{1,12}(\.[0-9]{1,9})?([KMGTPE]i?|k)?$`)
var snapshotReason = regexp.MustCompile(`^[A-Za-z]{1,64}$`)
var snapshotSysfsDenied = regexp.MustCompile(`(?is)sysfs.*(permission denied|operation not permitted|too revealing)`)
var snapshotConditionStatuses = []string{snapshotTrue, "False", "Unknown"}

// snapshotFluxNames are the Flux objects the installation creates itself.
var snapshotFluxNames = append(
	[]string{platformKustomization, "loco-worker", platformSource, fixtureRepoSource},
	platformLayers...,
)

type snapshotObject struct {
	Kind     string `json:"kind"`
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
	Note    string `json:"note"`
	Status  struct {
		Phase      string `json:"phase"`
		Conditions []struct {
			Type    string `json:"type"`
			Status  string `json:"status"`
			Reason  string `json:"reason"`
			Message string `json:"message"`
		} `json:"conditions"`
		Allocatable struct {
			CPU    string `json:"cpu"`
			Memory string `json:"memory"`
		} `json:"allocatable"`
		InitContainerStatuses []snapshotContainer `json:"initContainerStatuses"`
		ContainerStatuses     []snapshotContainer `json:"containerStatuses"`
	} `json:"status"`
}

type snapshotContainer struct {
	State struct {
		Waiting struct {
			Reason  string `json:"reason"`
			Message string `json:"message"`
		} `json:"waiting"`
		Terminated struct {
			Reason string `json:"reason"`
		} `json:"terminated"`
	} `json:"state"`
}

func (f *fixture) failureSnapshot(ctx context.Context) {
	counts := map[string]int{}
	for _, args := range [][]string{
		{kubectlGet, "pods,jobs,builds,events", "--all-namespaces"},
		{kubectlGet, "nodes"},
		{kubectlGet, "kustomizations,helmreleases,ocirepositories", "--all-namespaces"},
	} {
		args = append(args, "-o", "json")
		invocation := append([]string{"--kubeconfig", f.kubeconfig}, args...)
		cmd := exec.CommandContext(ctx, "kubectl", invocation...)
		cmd.Dir = f.root
		var output boundedOutput
		cmd.Stdout, cmd.Stderr = &output, io.Discard
		var result struct {
			Items []snapshotObject `json:"items"`
		}
		err := cmd.Run()
		data := output.Bytes()
		if err != nil || output.exceeded || json.Unmarshal(data, &result) != nil {
			counts["QueryUnavailable"]++
			continue
		}
		for _, object := range result.Items {
			countSnapshotState(counts, object)
		}
	}
	keys := maps.Keys(counts)
	states := slices.Sorted(keys)
	for _, state := range states {
		fmt.Printf("installation-e2e snapshot: %s count=%d\n", state, counts[state])
	}
}

func countSnapshotState(counts map[string]int, object snapshotObject) {
	switch object.Kind {
	case "Build":
		// phase strings are owned by k8sapi/v1alpha1/build_types.go.
		if slices.Contains(
			[]string{"Pending", "Running", "Succeeded", snapshotFailed, "Canceled"}, object.Status.Phase,
		) {
			counts["BuildPhase="+object.Status.Phase]++
		}
	case "Job":
		for _, condition := range object.Status.Conditions {
			failed := condition.Type == snapshotFailed && condition.Status == snapshotTrue
			if failed && condition.Reason == "DeadlineExceeded" {
				counts["JobFailedDeadlineExceeded"]++
			}
		}
	case "Pod":
		if slices.Contains(
			[]string{"Pending", "Running", "Succeeded", snapshotFailed, "Unknown"}, object.Status.Phase,
		) {
			counts["PodPhase="+object.Status.Phase]++
		}
		for _, condition := range object.Status.Conditions {
			if condition.Type == snapshotReady && slices.Contains(snapshotConditionStatuses, condition.Status) {
				counts["PodCondition=Ready status="+condition.Status]++
			}
			unscheduled := condition.Type == "PodScheduled" && condition.Status == "False"
			if unscheduled && condition.Reason == "Unschedulable" {
				counts["PodScheduledUnschedulable"]++
				countInsufficientResources(counts, condition.Message)
			}
		}
		for _, container := range slices.Concat(object.Status.InitContainerStatuses, object.Status.ContainerStatuses) {
			if snapshotSysfsDenied.MatchString(container.State.Waiting.Message) {
				counts["SysfsMountDenied"]++
			}
			reason := container.State.Waiting.Reason
			if slices.Contains([]string{
				"ContainerCreating", "PodInitializing", "ErrImagePull", "ImagePullBackOff", "CrashLoopBackOff",
				"CreateContainerConfigError", "CreateContainerError", "RunContainerError", "InvalidImageName",
			}, reason) {
				counts["ContainerWaiting="+reason]++
			}
			if container.State.Terminated.Reason == "OOMKilled" {
				counts["ContainerTerminated=OOMKilled"]++
			}
		}
	case "Event":
		countEventState(counts, object)
	case "Kustomization", "HelmRelease", "OCIRepository":
		countFluxState(counts, object)
	case "Node":
		cpu, memory := object.Status.Allocatable.CPU, object.Status.Allocatable.Memory
		if snapshotCPU.MatchString(cpu) && snapshotMemory.MatchString(memory) {
			counts["NodeAllocatable cpu="+cpu+" memory="+memory]++
		}
		for _, condition := range object.Status.Conditions {
			known := slices.Contains([]string{
				snapshotReady, "MemoryPressure", "DiskPressure", "PIDPressure", "NetworkUnavailable",
			}, condition.Type)
			if known && slices.Contains(snapshotConditionStatuses, condition.Status) {
				counts["NodeCondition="+condition.Type+" status="+condition.Status]++
			}
		}
	}
}

func countEventState(counts map[string]int, object snapshotObject) {
	if snapshotSysfsDenied.MatchString(object.Message + object.Note) {
		counts["SysfsMountDenied"]++
	}
	if slices.Contains([]string{
		"FailedScheduling", "FailedMount", "FailedAttachVolume", "FailedCreatePodSandBox",
		"FailedCreate", snapshotFailed, "BackOff", "DeadlineExceeded",
	}, object.Reason) {
		counts["EventReason="+object.Reason]++
	}
	if object.Reason == "FailedScheduling" {
		countInsufficientResources(counts, object.Message+object.Note)
	}
}

func countFluxState(counts map[string]int, object snapshotObject) {
	name := "other"
	if slices.Contains(snapshotFluxNames, object.Metadata.Name) {
		name = object.Metadata.Name
	}
	for _, condition := range object.Status.Conditions {
		if condition.Type != snapshotReady || condition.Status == snapshotTrue ||
			!slices.Contains(snapshotConditionStatuses, condition.Status) {
			continue
		}
		reason := "unknown"
		if snapshotReason.MatchString(condition.Reason) {
			reason = condition.Reason
		}
		counts["Flux "+object.Kind+"/"+name+" Ready="+condition.Status+" reason="+reason]++
	}
}

func countInsufficientResources(counts map[string]int, message string) {
	if strings.Contains(message, "Insufficient cpu") {
		counts["InsufficientCPU"]++
	}
	if strings.Contains(message, "Insufficient memory") {
		counts["InsufficientMemory"]++
	}
}
