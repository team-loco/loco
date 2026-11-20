package apps

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/team-loco/loco/internal/locofile"
	"github.com/team-loco/loco/tests/e2e/platform"
)

// endpointTimeout bounds how long the Service takes to route to a new rollout's pod.
const endpointTimeout = time.Minute

// serveArgScript runs the fixture's nginx from /tmp and serves its first argument at /<argument>.
const serveArgScript = `mkdir -p /tmp/www
printf ok > /tmp/www/index.html
printf %s "$1" > "/tmp/www/$1"
sed 's@/etc/nginx/conf.d/\*.conf@/tmp/e2e-server.conf@' /etc/nginx/nginx.conf > /tmp/e2e-nginx.conf
sed 's@/usr/share/nginx/html@/tmp/www@' /etc/nginx/conf.d/default.conf > /tmp/e2e-server.conf
exec nginx -c /tmp/e2e-nginx.conf -g 'daemon off;'`

// TestCommandAndArgs checks each rollout of a service serves the marker its args name.
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
		platform.Eventually(t, endpointTimeout, "the service to serve only the marker "+marker+" its args name",
			func(context.Context) error {
				if !p.Reaches(t, probe, "http://"+app+"/"+marker) {
					return errors.New("the marker is not served")
				}
				if previous != "" && p.Reaches(t, probe, "http://"+app+"/"+previous) {
					return errors.New("the previous marker is still served")
				}
				return nil
			})
		previous = marker
	}
}
