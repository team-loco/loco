package main

import agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"

// Each entry contains a UUID and small scalar fields. A thousand entries fit well below
// the transport's 4 MiB message limit, including inventories with both placements and builds.
const inventoryChunkEntries = 1000

func sendInventoryChunks(inventory *agentv1.Inventory, send func(*agentv1.SyncRequest) error) error {
	entries, builds := inventory.GetEntries(), inventory.GetBuilds()
	for {
		n := min(len(entries), inventoryChunkEntries)
		m := min(len(builds), inventoryChunkEntries-n)
		chunk := &agentv1.Inventory{Entries: entries[:n], Builds: builds[:m]}
		entries, builds = entries[n:], builds[m:]
		chunk.Complete = len(entries) == 0 && len(builds) == 0
		if err := send(inventoryRequest(chunk)); err != nil {
			return err
		}
		if chunk.GetComplete() {
			return nil
		}
	}
}
