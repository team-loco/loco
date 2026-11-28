package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"google.golang.org/protobuf/proto"

	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	"github.com/team-loco/loco/gen/go/loco/agent/v1/agentv1connect"
)

type inventoryServer struct {
	agentv1connect.UnimplementedAgentServiceHandler
	received chan *agentv1.Inventory
}

func (s *inventoryServer) Sync(_ context.Context, stream agentv1connect.AgentServiceSyncServerStream) error {
	inventory := &agentv1.Inventory{}
	for {
		msg, err := stream.Receive()
		if err != nil {
			return err
		}
		chunk := msg.GetInventoryChunk()
		inventory.Entries = append(inventory.Entries, chunk.GetEntries()...)
		inventory.Builds = append(inventory.Builds, chunk.GetBuilds()...)
		if chunk.GetComplete() {
			s.received <- inventory
			return stream.Send(&agentv1.SyncResponse{})
		}
	}
}

func TestInventoryFitsTheHTTPTransportAtOneHundredThousandPlacements(t *testing.T) {
	for _, count := range []int{0, 100000} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			inventory := &agentv1.Inventory{}
			for i := range count {
				inventory.Entries = append(inventory.Entries, &agentv1.InventoryEntry{
					PlacementId: fmt.Sprintf("00000000-0000-0000-0000-%012d", i), Revision: 1,
				})
			}
			if count != 0 {
				for i := range 1001 {
					inventory.Builds = append(inventory.Builds, &agentv1.InventoryBuild{
						BuildId: fmt.Sprintf("00000000-0000-0000-0000-%012d", i),
						Phase:   agentv1.BuildPhase_BUILD_PHASE_RUNNING,
					})
				}
				if size := proto.Size(inventoryRequest(inventory)); size <= 4<<20 {
					t.Fatalf("fixture size %d does not reproduce the transport failure", size)
				}
			}
			handler := &inventoryServer{received: make(chan *agentv1.Inventory, 1)}
			server := connect.NewServer()
			agentv1connect.RegisterAgentServiceHandler(server, handler)
			mux := http.NewServeMux()
			connecthttp.Mount(mux, server)
			srv := httptest.NewUnstartedServer(mux)
			srv.EnableHTTP2 = true
			srv.StartTLS()
			t.Cleanup(srv.Close)
			transport := connecthttp.NewTransport(srv.Client(), srv.URL)
			client := agentv1connect.NewAgentServiceClient(connect.NewClient(transport))
			stream, err := client.Sync(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			err = sendInventoryChunks(inventory, stream.Send)
			if err != nil {
				t.Fatalf("send inventory: %v", err)
			}
			if _, err = stream.Receive(); err != nil {
				t.Fatalf("receive inventory acknowledgment: %v", err)
			}
			if got := <-handler.received; !proto.Equal(got, inventory) {
				t.Fatal("HTTP inventory lost or duplicated placements or builds")
			}
			t.Logf("default 4 MiB transport accepted %d placements and %d builds", count, len(inventory.GetBuilds()))
		})
	}
}

func TestInventoryStopsAfterAFailedChunk(t *testing.T) {
	inventory := &agentv1.Inventory{Entries: make([]*agentv1.InventoryEntry, 3000)}
	for i := range inventory.GetEntries() {
		inventory.Entries[i] = &agentv1.InventoryEntry{PlacementId: "00000000-0000-0000-0000-000000000001", Revision: 1}
	}
	failed := errors.New("stream disconnected")
	calls := 0
	err := sendInventoryChunks(inventory, func(msg *agentv1.SyncRequest) error {
		calls++
		if msg.GetInventoryChunk().GetComplete() {
			t.Fatal("failed inventory was marked complete")
		}
		if calls == 2 {
			return failed
		}
		return nil
	})
	if !errors.Is(err, failed) || calls != 2 {
		t.Fatalf("send = %v after %d calls, want the second chunk's error", err, calls)
	}
}
