package apps

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/team-loco/loco/internal/locofile"
	"github.com/team-loco/loco/tests/e2e/platform"
)

func TestImageUsersRunInUserNamespaces(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)
	source, err := name.ParseReference(p.PublicImage, name.Insecure)
	if err != nil {
		t.Fatal(err)
	}
	image, err := remote.Image(source, remote.WithContext(t.Context()))
	if err != nil {
		t.Fatal(err)
	}
	config, err := image.ConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, tc := range []struct {
		name    string
		user    string
		wantUID string
	}{
		{name: "named", user: "nginx", wantUID: config.Config.User},
		{name: "root", user: "0", wantUID: "0"},
		{name: "unspecified", wantUID: "0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			variant := config.DeepCopy()
			variant.Config.User = tc.user
			modified, err := mutate.ConfigFile(image, variant)
			if err != nil {
				t.Fatal(err)
			}
			ref := source.Context().Tag("user-" + w.ID + "-" + tc.name)
			if err := remote.Write(ref, modified, remote.WithContext(t.Context())); err != nil {
				t.Fatal(err)
			}
			service := platform.ImageService(ref.Name())
			platform.WriteLocoFile(t, dir, locofile.File{
				Partial: "image-users", Services: map[string]locofile.Service{"web": service},
			})
			w.MustLoco(t, dir, "deploy", "--yes", "--wait")
			deployment := "deployment/resource-" + w.ResourceID(t, "web")
			exec := []string{"-n", w.Namespace, "exec", deployment, "--"}
			uid := p.MustKubectl(t, append(exec, "id", "-u")...)
			if strings.TrimSpace(uid) != tc.wantUID {
				t.Fatalf("UID = %q, want %q", uid, tc.wantUID)
			}
			mapping := p.MustKubectl(t, append(exec, "cat", "/proc/self/uid_map")...)
			var inside, outside, count uint64
			if _, err := fmt.Sscan(mapping, &inside, &outside, &count); err != nil {
				t.Fatal(err)
			}
			isolatedRange := inside == 0 && outside >= 65536 && count == 65536
			if !isolatedRange {
				t.Fatalf("pod lacks an isolated UID range: %q", mapping)
			}
			p.MustKubectl(t, append(exec, "curl", "--fail", "--silent", "--max-time", "5", "http://127.0.0.1:8080/")...)
		})
	}
}
