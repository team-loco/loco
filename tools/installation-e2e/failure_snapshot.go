package main

import (
	"context"
	"encoding/json"
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

var snapshotCPU = regexp.MustCompile(`^[0-9]{1,12}(\.[0-9]{1,9})?m?$`)
var snapshotMemory = regexp.MustCompile(`^[0-9]{1,12}(\.[0-9]{1,9})?([KMGTPE]i?|k)?$`)

type snapshotObject struct {
	Kind    string `json:"kind"`
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
			Reason string `json:"reason"`
		} `json:"waiting"`
		Terminated struct {
			Reason string `json:"reason"`
		} `json:"terminated"`
	} `json:"state"`
}

func (f *fixture) failureSnapshot(ctx context.Context) {
	counts := map[string]int{}
	namespace, err := f.pin(ctx, "charts/loco-worker/values.yaml", ".builds.namespace")
	if err != nil {
		fmt.Println("installation-e2e snapshot: unavailable")
		return
	}
	for _, args := range [][]string{
		{"-n", namespace, "get", "pods,jobs,builds,events"},
		{"get", "nodes"},
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
			failed := condition.Type == snapshotFailed && condition.Status == "True"
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
			unscheduled := condition.Type == "PodScheduled" && condition.Status == "False"
			if unscheduled && condition.Reason == "Unschedulable" {
				counts["PodScheduledUnschedulable"]++
				countInsufficientResources(counts, condition.Message)
			}
		}
		for _, container := range slices.Concat(object.Status.InitContainerStatuses, object.Status.ContainerStatuses) {
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
		if slices.Contains([]string{
			"FailedScheduling", "FailedMount", "FailedAttachVolume", "FailedCreatePodSandBox",
			"FailedCreate", snapshotFailed, "BackOff", "DeadlineExceeded",
		}, object.Reason) {
			counts["EventReason="+object.Reason]++
		}
		if object.Reason == "FailedScheduling" {
			countInsufficientResources(counts, object.Message+object.Note)
		}
	case "Node":
		cpu, memory := object.Status.Allocatable.CPU, object.Status.Allocatable.Memory
		if snapshotCPU.MatchString(cpu) && snapshotMemory.MatchString(memory) {
			counts["NodeAllocatable cpu="+cpu+" memory="+memory]++
		}
		for _, condition := range object.Status.Conditions {
			known := slices.Contains([]string{
				"Ready", "MemoryPressure", "DiskPressure", "PIDPressure", "NetworkUnavailable",
			}, condition.Type)
			if known && slices.Contains([]string{"True", "False", "Unknown"}, condition.Status) {
				counts["NodeCondition="+condition.Type+" status="+condition.Status]++
			}
		}
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
