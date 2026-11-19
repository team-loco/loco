package apps

import (
	"testing"

	"github.com/team-loco/loco/internal/locofile"
	"github.com/team-loco/loco/tests/e2e/platform"
)

// serveArgScript serves its first argument at /<argument> from a copy of the fixture's nginx
// configuration rooted in /tmp.
const serveArgScript = `mkdir -p /tmp/www
printf ok > /tmp/www/index.html
printf %s "$1" > "/tmp/www/$1"
sed 's@/etc/nginx/conf.d/\*.conf@/tmp/e2e-server.conf@' /etc/nginx/nginx.conf > /tmp/e2e-nginx.conf
sed 's@/usr/share/nginx/html@/tmp/www@' /etc/nginx/conf.d/default.conf > /tmp/e2e-server.conf
exec nginx -c /tmp/e2e-nginx.conf -g 'daemon off;'`

// TestCommandAndArgs deploys a service whose command and args replace the image's entrypoint and
// CMD, then changes the args, and checks each rollout serves what its args name.
func TestCommandAndArgs(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)
	const app = "e2e-command"
	dir := t.TempDir()
	service := platform.ImageService(p.PublicImage)
	service.Command = []string{"sh", "-ec", serveArgScript, "serve-arg"}
	file := locofile.File{Partial: app, Services: map[string]locofile.Service{app: service}}

	probe := platform.ProbePod{Namespace: w.Namespace, Name: "command-probe"}
	p.DeleteOnCleanup(t, "-n", w.Namespace, "pod", probe.Name)
	var previous string
	for range 2 {
		marker := platform.RandomHex(8)
		service.Args = []string{marker}
		file.Services[app] = service
		platform.WriteLocoFile(t, dir, file)
		w.MustLoco(t, dir, "deploy", "--yes", "--wait")
		if !p.Reaches(t, probe, "http://"+app+"/"+marker) {
			t.Fatalf("the service does not serve the marker %s its args name", marker)
		}
		if previous != "" && p.Reaches(t, probe, "http://"+app+"/"+previous) {
			t.Fatalf("the service still serves the marker %s of its previous args", previous)
		}
		previous = marker
	}
}
