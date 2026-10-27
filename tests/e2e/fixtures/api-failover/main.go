package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"sync"
	"time"
)

const (
	listenAddress     = ":18080"
	readHeaderTimeout = 5 * time.Second
)

var version = "(devel)"

type receipt struct {
	Bytes  int    `json:"bytes"`
	SHA256 string `json:"sha256"`
}

func main() {
	var mu sync.Mutex
	ledger := map[string][]receipt{}
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			mu.Lock()
			defer mu.Unlock()
			if err := json.NewEncoder(w).Encode(ledger); err != nil {
				log.Print(err)
			}
			return
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var body struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &body); err != nil || body.ID == "" {
			http.Error(w, "missing request id", http.StatusBadRequest)
			return
		}
		digest := sha256.Sum256(raw)
		mu.Lock()
		ledger[body.ID] = append(ledger[body.ID], receipt{len(raw), hex.EncodeToString(digest[:])})
		mu.Unlock()
		status := http.StatusOK
		if r.URL.Path == "/error" {
			status = http.StatusInternalServerError
		}
		if r.URL.Path == "/handled-503" {
			status = http.StatusServiceUnavailable
		}
		w.WriteHeader(status)
		if err := json.NewEncoder(w).Encode(body); err != nil {
			log.Print(err)
		}
	})
	log.Printf("api failover fixture %s", version)
	server := &http.Server{Addr: listenAddress, ReadHeaderTimeout: readHeaderTimeout}
	log.Fatal(server.ListenAndServe())
}
