package controller

import (
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/types"
)

func TestDeploymentEventIdentityDoesNotChangeThePodTemplate(t *testing.T) {
	app := telemetryTestApplication()
	app.UID = types.UID("application-before")
	before := buildDeployment(t, app)
	app.UID = types.UID("application-after")
	after := buildDeployment(t, app)
	if got := after.Annotations["loco.io/application-uid"]; got != string(app.UID) {
		t.Fatalf("deployment Application identity=%q, want %q", got, app.UID)
	}
	if !reflect.DeepEqual(before.Spec.Template, after.Spec.Template) {
		t.Fatal("event identity changed the pod template and would trigger a rollout")
	}
	if _, ok := after.Spec.Template.Annotations["loco.io/application-uid"]; ok {
		t.Fatal("pod template carries rollout-triggering Application UID")
	}
}
