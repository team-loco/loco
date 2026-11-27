package routing

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
	"github.com/team-loco/loco/internal/locofile"
	"github.com/team-loco/loco/tests/e2e/platform"
)

const caPlacementDeletedSQL = `SELECT desired_deleted FROM placements p
JOIN resources r ON r.id=p.resource_id WHERE r.workspace_id=$1 AND r.name='ca'`

type providerFixture struct {
	mu      sync.Mutex
	records map[string]map[string]any
	deletes int
}

func (f *providerFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	result := any(map[string]string{})
	switch r.Method {
	case http.MethodGet:
		records := []map[string]any{}
		if value, ok := f.records[r.URL.Query().Get("name.exact")]; ok {
			records = append(records, value)
		}
		result = records
	case http.MethodPost, http.MethodPatch:
		var value map[string]any
		if err := json.NewDecoder(r.Body).Decode(&value); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		name, ok := value["name"].(string)
		if !ok {
			http.Error(w, "missing name", http.StatusBadRequest)
			return
		}
		value["id"] = name
		f.records[name] = value
		result = value
	case http.MethodDelete:
		name := filepath.Base(r.URL.Path)
		delete(f.records, name)
		f.deletes++
	default:
		http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
		return
	}
	if err := json.NewEncoder(w).Encode(map[string]any{"success": true, "result": result}); err != nil {
		return
	}
}

// The provider is synthetic. The child runs production publisher code against
// the installed API with an owned token; its ACKs release actual retirement.
func exerciseInstalledPublisher(t *testing.T, w *platform.Workspace, dir, binding string, file locofile.File) {
	t.Helper()
	entity := tokenv1.EntityType_ENTITY_TYPE_ROUTING_PUBLISHER
	token := platform.CreateToken(t, p.Admin, "routing-e2e", entity, binding,
		platform.Scopes(entity, binding, tokenv1.Scope_SCOPE_READ, tokenv1.Scope_SCOPE_WRITE)...)
	scratch := t.TempDir()
	tokenFile := filepath.Join(scratch, "loco-token")
	providerFile := filepath.Join(scratch, "provider-token")
	for path, value := range map[string]string{tokenFile: token, providerFile: "synthetic-provider-token"} {
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	fixture := &providerFixture{records: map[string]map[string]any{}}
	server := httptest.NewServer(http.HandlerFunc(fixture.serve))
	defer server.Close()
	var clusters []string
	if err := p.DB.QueryRow(
		t.Context(),
		"SELECT cluster_ids FROM public_route_publications WHERE hostname=$1",
		w.Host(sharedService),
	).Scan(&clusters); err != nil {
		t.Fatal(err)
	}
	destinations := make([]map[string]any, 0, 3)
	other := []string{}
	for _, id := range clusters {
		if id != p.ClusterID {
			other = append(other, id)
		}
	}
	for name, ids := range map[string][]string{"ca": {p.ClusterID}, "ny": other, sharedService: clusters} {
		destinations = append(
			destinations,
			map[string]any{"clusterIDs": ids, "hostname": "infra-" + name + "." + p.Domain},
		)
	}
	cfg := map[string]any{"providerURL": server.URL, "config": map[string]any{
		"apiURL": p.APIURL, "pollSeconds": 1, "auditSeconds": 100, "batchSize": 3, "requestSeconds": 10,
		"bindings": []map[string]any{{"appDomain": p.Domain, "publisherID": binding, "zoneID": strings.Repeat("a", 32),
			"locoTokenFile": tokenFile, "cloudflareTokenFile": providerFile, "destinations": destinations}},
	}}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(scratch, "config.json")
	if err = os.WriteFile(configFile, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	run := func() {
		t.Helper()
		// Expire the synthetic writer lease before handing the binding to the child.
		if _, err := p.DB.Exec(
			t.Context(),
			"UPDATE route_publishers SET session_expires_at=NOW()-INTERVAL '1 second' WHERE id=$1",
			binding,
		); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(
			ctx,
			"go",
			"-C",
			"../../../integrations/cloudflare",
			"test",
			"-v",
			"-count=1",
			"-run",
			"^TestPublisherInstalledCore$",
		)
		cmd.Env = append(os.Environ(), "LOCO_PUBLISHER_E2E_CONFIG="+configFile)
		output, runErr := cmd.CombinedOutput()
		t.Logf("installed publisher: %s", output)
		if runErr != nil {
			t.Fatal(runErr)
		}
	}
	run()
	fixture.mu.Lock()
	for name, target := range map[string]string{"ca": "ca", "ny": "ny", sharedService: sharedService} {
		value := fixture.records[w.Host(name)]
		if value["content"] != "infra-"+target+"."+p.Domain || value["proxied"] != true {
			t.Errorf("provider record for %s: %v", name, value)
		}
	}
	fixture.mu.Unlock()
	ca := file.Services["ca"]
	enabled := false
	ca.Environments = map[string]locofile.Override{platform.Production: {Enabled: &enabled}}
	file.Services["ca"] = ca
	platform.WriteLocoFile(t, dir, file)
	w.MustLoco(t, dir, "infra", "apply", "--yes", "--confirm-destructive")
	platform.Eventually(t, routeWait, "CA withdrawal intent", func(ctx context.Context) error {
		var withdrawing bool
		if err := p.DB.QueryRow(ctx, "SELECT withdrawing FROM public_route_publications WHERE hostname=$1", w.Host("ca")).
			Scan(&withdrawing); err != nil {
			return err
		}
		if !withdrawing {
			return errors.New("withdrawal not staged")
		}
		return nil
	})
	assertPlacementRetained(t, w)
	run()
	fixture.mu.Lock()
	_, exists := fixture.records[w.Host("ca")]
	deletes := fixture.deletes
	fixture.mu.Unlock()
	if exists || deletes != 1 {
		t.Fatalf("withdrawal record exists=%v deletes=%d", exists, deletes)
	}
	var acknowledged bool
	if err := p.DB.QueryRow(
		t.Context(),
		"SELECT acknowledged_revision=revision FROM public_route_publications WHERE hostname=$1",
		w.Host("ca"),
	).Scan(&acknowledged); err != nil {
		t.Fatal(err)
	}
	if !acknowledged {
		t.Fatal("publisher did not ACK withdrawal against installed core")
	}
	assertPlacementRetained(t, w)
	// Advance only this synthetic tenant's drain deadline.
	if _, err := p.DB.Exec(
		t.Context(),
		"UPDATE public_route_publications SET drain_until=NOW()-INTERVAL '1 second' WHERE hostname=$1",
		w.Host("ca"),
	); err != nil {
		t.Fatal(err)
	}
	platform.Eventually(
		t,
		routeWait,
		"placement deletion after publisher ACK and drain",
		func(ctx context.Context) error {
			var deleted bool
			if err := p.DB.QueryRow(ctx, caPlacementDeletedSQL, w.ID).
				Scan(&deleted); err != nil {
				return err
			}
			if !deleted {
				return errors.New("placement still retained")
			}
			return nil
		},
	)
	t.Log(
		"Publisher created exact-set provider records, acknowledged withdrawal, and released retirement only after drain",
	)
}

func assertPlacementRetained(t *testing.T, w *platform.Workspace) {
	t.Helper()
	var deleted bool
	if err := p.DB.QueryRow(t.Context(), caPlacementDeletedSQL, w.ID).
		Scan(&deleted); err != nil {
		t.Fatal(err)
	}
	if deleted {
		t.Fatal("serving placement deleted before withdrawal acknowledgement and drain")
	}
}
