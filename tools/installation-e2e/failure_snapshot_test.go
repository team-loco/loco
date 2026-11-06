package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFailedOperatorSnapshotsOnlySafeStateAndKeepsExitError(t *testing.T) {
	bin := t.TempDir()
	state := `{"items":[
{"kind":"Build","metadata":{"name":"credential-canary"},
 "spec":{"token":"credential-canary"},"status":{"phase":"Running"}},
{"kind":"Build","status":{"phase":"Running credential-canary"}},
{"kind":"Job","status":{"conditions":[{"type":"Failed","status":"True",
 "reason":"DeadlineExceeded","message":"credential-canary"}]}},
{"kind":"Pod","status":{"phase":"Pending","conditions":[{"type":"PodScheduled","status":"False",
 "reason":"Unschedulable","message":"Insufficient cpu, Insufficient memory credential-canary"}],
 "containerStatuses":[{"name":"credential-canary",
 "state":{"waiting":{"reason":"ImagePullBackOff","message":"credential-canary"}}},
 {"state":{"terminated":{"reason":"OOMKilled","message":"credential-canary"}}},
 {"state":{"waiting":{"reason":"CrashLoopBackOff credential-canary"}}},
 {"state":{"waiting":null,"terminated":null}}]}},
{"kind":"Event","reason":"FailedScheduling","message":"Insufficient cpu credential-canary",
 "involvedObject":{"name":"credential-canary"}},
{"kind":"Event","reason":"BackOff credential-canary","message":"credential-canary"}
]}`
	nodes := `{"items":[
{"kind":"Node","metadata":{"name":"credential-canary"},"status":{
 "allocatable":{"cpu":"4000m","memory":"8Gi"},"conditions":[
 {"type":"Ready","status":"False","reason":"credential-canary","message":"credential-canary"},
 {"type":"MemoryPressure","status":"True"},{"type":"DiskPressure","status":"False"},
 {"type":"PIDPressure","status":"False"},{"type":"NetworkUnavailable","status":"Unknown"},
 {"type":"Ready credential-canary","status":"True"},{"type":"PIDPressure","status":"True credential-canary"}]}},
{"kind":"Node","status":{"allocatable":{"cpu":"credential-canary","memory":"8Gi credential-canary"}}}
]}`
	t.Setenv("SNAPSHOT_TEST_JSON", state)
	t.Setenv("SNAPSHOT_TEST_NODES", nodes)
	calls := filepath.Join(t.TempDir(), "calls")
	t.Setenv("SNAPSHOT_TEST_CALLS", calls)
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$SNAPSHOT_TEST_CALLS"
case "$*" in
*'exec operator'*) printf 'installation-e2e phase: source-deploy\ncredential-canary\n'; exit 17 ;;
esac
if [ "$SNAPSHOT_TEST_FAIL" = 1 ]; then printf 'credential-canary' >&2; exit 42; fi
case "$*" in
*'get nodes'*) printf '%s' "$SNAPSHOT_TEST_NODES" ;;
*) printf '%s' "$SNAPSHOT_TEST_JSON" ;;
esac
`
	for name, source := range map[string]string{"kubectl": script, "yq": "#!/bin/sh\nprintf 'loco-builds\\n'\n"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(source), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	output, err := os.CreateTemp(t.TempDir(), "snapshot-output")
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	previousOut, previousErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = output, output
	defer func() { os.Stdout, os.Stderr = previousOut, previousErr }()
	f := fixture{root: t.TempDir(), kubeconfig: "/owned/kubeconfig"}
	var wantOutput string
	for _, tc := range []struct{ fail, snapshot string }{
		{"", `installation-e2e snapshot: BuildPhase=Running count=1
installation-e2e snapshot: ContainerTerminated=OOMKilled count=1
installation-e2e snapshot: ContainerWaiting=ImagePullBackOff count=1
installation-e2e snapshot: EventReason=FailedScheduling count=1
installation-e2e snapshot: InsufficientCPU count=2
installation-e2e snapshot: InsufficientMemory count=1
installation-e2e snapshot: JobFailedDeadlineExceeded count=1
installation-e2e snapshot: NodeAllocatable cpu=4000m memory=8Gi count=1
installation-e2e snapshot: NodeCondition=DiskPressure status=False count=1
installation-e2e snapshot: NodeCondition=MemoryPressure status=True count=1
installation-e2e snapshot: NodeCondition=NetworkUnavailable status=Unknown count=1
installation-e2e snapshot: NodeCondition=PIDPressure status=False count=1
installation-e2e snapshot: NodeCondition=Ready status=False count=1
installation-e2e snapshot: PodPhase=Pending count=1
installation-e2e snapshot: PodScheduledUnschedulable count=1
`},
		{"1", "installation-e2e snapshot: QueryUnavailable count=2\n"},
	} {
		t.Setenv("SNAPSHOT_TEST_FAIL", tc.fail)
		err = f.runOperator(t.Context())
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 17 || strings.Contains(err.Error(), "credential-canary") {
			t.Fatalf("case %q lost the original exit status or exposed private output", tc.fail)
		}
		data, readErr := os.ReadFile(output.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		wantOutput += "installation-e2e phase: source-deploy\n" + tc.snapshot
		if string(data) != wantOutput {
			t.Fatalf("case %q did not emit only the safe snapshot", tc.fail)
		}
	}
	invocations, readErr := os.ReadFile(calls)
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, want := range []string{
		"--kubeconfig /owned/kubeconfig -n loco-builds get pods,jobs,builds,events -o json",
		"--kubeconfig /owned/kubeconfig get nodes -o json",
	} {
		if !strings.Contains(string(invocations), want) {
			t.Errorf("snapshot did not query only its cluster: %q", invocations)
		}
	}
}
