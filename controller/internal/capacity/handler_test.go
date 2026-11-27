package capacity

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/team-loco/loco/internal/capacitymetrics"
	locolabels "github.com/team-loco/loco/k8sapi/labels"
)

func assigned(name string) *corev1.Pod {
	return &corev1.Pod{
		Name: name, Namespace: "workload", UID: types.UID(uuid.NewString()),
		Spec: corev1.PodSpec{
			NodeName: "node-a",
			Containers: []corev1.Container{
				{
					Name: "app",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("200m"),
							corev1.ResourceMemory: resource.MustParse("128Mi"),
						},
					},
				},
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

func leader() <-chan struct{} {
	elected := make(chan struct{})
	close(elected)
	return elected
}

func TestHandlerReportsEffectiveRequestsAndCompleteMembership(t *testing.T) {
	t.Parallel()
	pod := assigned("runtime")
	workspace, environment, resourceID, org := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	pod.Labels = map[string]string{
		locolabels.LabelManagedBy:      locolabels.ControllerManagedBy,
		locolabels.LabelWorkspaceID:    workspace,
		locolabels.LabelEnvironmentID:  environment,
		locolabels.LabelResourceID:     resourceID,
		locolabels.LabelOrganizationID: org,
	}
	pod.Spec.InitContainers = []corev1.Container{
		{
			Name: "init",
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("800m"),
					corev1.ResourceMemory: resource.MustParse("256Mi"),
				},
			},
		},
	}
	pod.Spec.Overhead = corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("100m"),
		corev1.ResourceMemory: resource.MustParse("64Mi"),
	}
	done := assigned("done")
	done.Status.Phase = corev1.PodSucceeded
	waiting := assigned("waiting")
	waiting.Spec.NodeName = ""
	system := assigned("system")
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	reader := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(pod, done, waiting, system).
		WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, options ...client.ListOption) error {
				opts := &client.ListOptions{}
				opts.ApplyOptions(options)
				if opts.Limit != pageSize {
					t.Fatal("pod collection is not paginated")
				}
				// The fake client cannot evaluate the API server's != phase selectors.
				opts.FieldSelector = nil
				return c.List(ctx, list, opts)
			},
		}).
		Build()
	response := httptest.NewRecorder()
	Handler(
		reader,
		leader(),
	).ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, capacitymetrics.Path, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	var runtimeCPU, runtimeMemory, manifest string
	for line := range strings.SplitSeq(body, "\n") {
		if strings.HasPrefix(line, capacitymetrics.CPU+"{") && strings.Contains(line, `pod="runtime"`) {
			runtimeCPU = line
		}
		if strings.HasPrefix(line, capacitymetrics.Memory+"{") && strings.Contains(line, `pod="runtime"`) {
			runtimeMemory = line
		}
		if strings.HasPrefix(line, capacitymetrics.Pods+"{") {
			manifest = line
		}
	}
	if pointValue(t, runtimeCPU) != 0.9 || pointValue(t, runtimeMemory) != 335544320 {
		t.Fatalf("effective requests: CPU=%s memory=%s", runtimeCPU, runtimeMemory)
	}
	if !strings.Contains(runtimeCPU, `workspace_id="`+workspace+`"`) ||
		!strings.Contains(runtimeCPU, `environment_id="`+environment+`"`) {
		t.Fatalf("lost workload identity: %s", runtimeCPU)
	}
	if pointValue(t, manifest) != 2 {
		t.Fatalf("complete membership=%s", manifest)
	}
	cpuFields, memoryFields, manifestFields := strings.Fields(
		runtimeCPU,
	), strings.Fields(
		runtimeMemory,
	), strings.Fields(
		manifest,
	)
	if cpuFields[len(cpuFields)-1] != memoryFields[len(memoryFields)-1] ||
		cpuFields[len(cpuFields)-1] != manifestFields[len(manifestFields)-1] {
		t.Fatal("collection timestamps differ")
	}
	if strings.Contains(body, `pod="done"`) || strings.Contains(body, `pod="waiting"`) {
		t.Fatal("terminal/unassigned pod consumes node capacity")
	}
}

type interruptedReader struct {
	client.Reader
	calls  int
	copies int
	pod    *corev1.Pod
}

func (r *interruptedReader) List(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
	r.calls++
	if r.calls > 1 {
		return errors.New("collection interrupted")
	}
	pods, ok := list.(*corev1.PodList)
	if !ok {
		return errors.New("unexpected list type")
	}
	for i := range max(1, r.copies) {
		samplePod := r.pod.DeepCopy()
		samplePod.UID = types.UID(uuid.NewString())
		samplePod.Name += "-" + strconv.Itoa(i)
		pods.Items = append(pods.Items, *samplePod)
	}
	pods.Continue = "next"
	return nil
}

func TestHandlerNeverPublishesPartialOrUnelectedCollections(t *testing.T) {
	t.Parallel()
	reader := &interruptedReader{pod: assigned("first"), copies: 500}
	response := httptest.NewRecorder()
	Handler(
		reader,
		make(chan struct{}),
	).ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, capacitymetrics.Path, nil))
	if response.Code != http.StatusOK || response.Body.Len() != 0 || reader.calls != 0 {
		t.Fatal("unelected source collected metrics")
	}
	server := httptest.NewServer(Handler(reader, leader()))
	defer server.Close()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	responseHTTP, err := server.Client().Do(request)
	if err == nil {
		defer responseHTTP.Body.Close()
		body, readErr := io.ReadAll(responseHTTP.Body)
		if readErr == nil {
			t.Fatalf("failed collection was accepted: %s", body)
		}
	}
}

func pointValue(t *testing.T, line string) float64 {
	t.Helper()
	fields := strings.Fields(line)
	if len(fields) < 3 {
		t.Fatalf("missing timestamped metric: %q", line)
	}
	value, err := strconv.ParseFloat(fields[len(fields)-2], 64)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestHandlerPreservesReservationsDuringResize(t *testing.T) {
	t.Parallel()
	const small = "200m"
	const large = "800m"
	for _, tc := range []struct {
		name       string
		spec       string
		allocated  string
		podLevel   bool
		infeasible bool
		want       float64
	}{
		{name: "container downsize", spec: small, allocated: large, want: 0.8},
		{name: "container upsize", spec: large, allocated: small, want: 0.8},
		{name: "infeasible upsize", spec: large, allocated: small, infeasible: true, want: 0.2},
		{name: "pod downsize", spec: small, allocated: large, podLevel: true, want: 0.8},
		{name: "pod upsize", spec: large, allocated: small, podLevel: true, want: 0.8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pod := assigned("resize")
			requested := corev1.ResourceList{corev1.ResourceCPU: resource.MustParse(tc.spec)}
			allocated := corev1.ResourceList{corev1.ResourceCPU: resource.MustParse(tc.allocated)}
			if tc.podLevel {
				pod.Spec.Resources = &corev1.ResourceRequirements{Requests: requested}
				pod.Status.Resources = &corev1.ResourceRequirements{Requests: allocated}
				pod.Status.AllocatedResources = allocated
			} else {
				pod.Spec.Containers[0].Resources.Requests = requested
				pod.Status.ContainerStatuses = []corev1.ContainerStatus{{
					Name: "app", AllocatedResources: allocated,
					Resources: &corev1.ResourceRequirements{Requests: allocated},
				}}
			}
			if tc.infeasible {
				pod.Status.Conditions = []corev1.PodCondition{
					{Type: corev1.PodResizePending, Reason: corev1.PodReasonInfeasible},
				}
			}
			reader := &interruptedReader{pod: pod}
			var collected strings.Builder
			c := collection{reader: reader, ctx: t.Context(), source: uuid.NewString(), at: time.Now()}
			if _, err := c.pods(&collected); err == nil || err.Error() != "list capacity pods: collection interrupted" {
				t.Fatalf("expected interrupted final page, got %v", err)
			}
			line, _, _ := strings.Cut(collected.String(), "\n")
			if got := pointValue(t, line); got != tc.want {
				t.Fatalf("CPU reservation=%v want %v", got, tc.want)
			}
		})
	}
}

func TestHandlerPreservesStandaloneBuildIdentity(t *testing.T) {
	t.Parallel()
	pod := assigned("standalone-build")
	pod.Labels = map[string]string{
		locolabels.LabelManagedBy:   locolabels.ControllerManagedBy,
		locolabels.LabelWorkspaceID: "standalone-workspace",
		locolabels.LabelResourceID:  "worker",
		locolabels.LabelBuildID:     "build-one",
	}
	var out strings.Builder
	c := collection{reader: &interruptedReader{pod: pod}, ctx: t.Context(), source: uuid.NewString(), at: time.Now()}
	if _, err := c.pods(&out); err == nil || err.Error() != "list capacity pods: collection interrupted" {
		t.Fatalf("identity rejected before final page: %v", err)
	}
	text := out.String()
	for _, label := range []string{
		`workspace_id="standalone-workspace"`, `resource_id="worker"`,
		`build_id="build-one"`, `organization_id=""`,
	} {
		if !strings.Contains(text, label) {
			t.Fatalf("lost valid standalone Build identity %s: %s", label, text)
		}
	}
}
