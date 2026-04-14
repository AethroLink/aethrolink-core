package main

import (
	"context"
	"flag"
	"log"
	"net/http"

	"github.com/aethrolink/aethrolink-core/internal/adapters"
	"github.com/aethrolink/aethrolink-core/internal/api"
	"github.com/aethrolink/aethrolink-core/internal/config"
	"github.com/aethrolink/aethrolink-core/internal/core"
	"github.com/aethrolink/aethrolink-core/internal/runtime"
	"github.com/aethrolink/aethrolink-core/internal/storage"
)

func main() {
	var (
		host         = flag.String("host", "127.0.0.1", "bind host")
		port         = flag.String("port", "7777", "bind port")
		registryPath = flag.String("registry", "configs/registry.yaml", "path to runtime registry")
		databaseURL  = flag.String("database", "sqlite://./aethrolink.db", "sqlite database path")
		artifactDir  = flag.String("artifact-dir", "artifacts", "artifact directory")
	)
	flag.Parse()

	registry, err := config.LoadRegistry(*registryPath)
	if err != nil {
		log.Fatalf("load registry: %v", err)
	}
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
	adapterRegistry := adapters.NewRegistry()
	adapterRegistry.Register("acp", adapters.NewACPAdapter(registry, runtimeManager))
	orchestrator := core.NewOrchestrator(registry, store, runtimeManager, adapterRegistry)
	if err := orchestrator.PreloadRegistry(context.Background()); err != nil {
		log.Fatalf("preload registry: %v", err)
	}
	addr := *host + ":" + *port
	log.Printf("aethrolink-go listening on http://%s", addr)
	if err := http.ListenAndServe(addr, api.NewServer(orchestrator)); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
