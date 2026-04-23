package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/aethrolink/aethrolink-core/internal/adapters"
	"github.com/aethrolink/aethrolink-core/internal/agents"
	"github.com/aethrolink/aethrolink-core/internal/api"
	"github.com/aethrolink/aethrolink-core/internal/core"
	"github.com/aethrolink/aethrolink-core/internal/runtime"
	"github.com/aethrolink/aethrolink-core/internal/storage"
)

func main() {
	var (
		host        = flag.String("host", "127.0.0.1", "bind host")
		port        = flag.String("port", "7777", "bind port")
		databaseURL = flag.String("database", "sqlite://./aethrolink.db", "sqlite database path")
		artifactDir = flag.String("artifact-dir", "artifacts", "artifact directory")
	)
	flag.Parse()
	baseURL := "http://" + *host + ":" + *port
	store, err := storage.Open(*databaseURL, *artifactDir, baseURL)
	if err != nil {
		log.Fatalf("open sqlite store: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			log.Printf("close store: %v", err)
		}
	}()

	// Composition root: runtime manager owns live workers, adapters translate
	// runtime-specific behavior, and the orchestrator owns task lifecycle.
	runtimeManager := runtime.NewManager(store)
	agentService := agents.NewService(store)
	adapterRegistry := adapters.NewRegistry()
	adapterRegistry.Register("acp", adapters.NewACPAdapter(agentService, runtimeManager))
	orchestrator, err := core.NewOrchestrator(agentService, store, runtimeManager, adapterRegistry)
	if err != nil {
		log.Fatalf("create orchestrator: %v", err)
	}
	addr := *host + ":" + *port
	log.Printf("aethrolink-go listening on http://%s", addr)
	if err := http.ListenAndServe(addr, api.NewServer(orchestrator, agentService)); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
