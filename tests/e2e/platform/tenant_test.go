package platform

import "testing"

func TestWorkspaceHost(t *testing.T) {
	first := Workspace{ID: "019a898d-0000-7000-8000-000000000001"}
	second := Workspace{ID: "019a898d-0000-7000-8000-000000000002"}
	const name = "e2e-no-builds-source"
	want := name + "-" + first.ID + "." + platformDomain
	if got := first.Host(name); got != want {
		t.Fatalf("hostname %q, want %q", got, want)
	}
	if first.Host(name) == second.Host(name) {
		t.Fatal("two workspaces claim the same fixture hostname")
	}
}
