package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"syscall"
	"time"

	"github.com/dioptra-io/retina-generator/internal/retina"
)

func main() {
	var (
		seed   = flag.Int64("seed", 42, "Seed for the random generator.")
		minTTL = flag.Uint("min_ttl", 4, "Minimum TTL value for generated PDs.")
		maxTTL = flag.Uint("max_ttl", 32, "Maximum TTL value for generated PDs.")
		// agentIDs = flag.String("agent_ids", "", "Comma-separated list of agent IDs.")
		numPDs = flag.Uint64("num_pds", 1, "Number of ProbingDirectives to generate.")

		orchestrator = flag.String("orchestrator", "localhost:8080", "Orchestrator base URL.")
		httpTimeout  = flag.Duration("http_timeout", 10*time.Second, "HTTP timeout (0 means no timeout).")
	)
	flag.Parse()

	// Get the agentIDs from positional arguments.
	agentIDs := flag.Args()

	// Setup the context from the signal handlers.
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	gen, err := retina.NewGenFromConfig(&retina.Config{
		Seed:                *seed,
		MinTTL:              uint8(*minTTL),
		MaxTTL:              uint8(*maxTTL),
		AgentIDs:            agentIDs,
		NumPDs:              *numPDs,
		OrchestratorAddress: *orchestrator,
		HTTPTimeout:         *httpTimeout,
	})
	if err != nil {
		log.Fatalf("cannot create generator with the provided config: %v", err)
	}

	// Run the generator until completion.
	if err := gen.Run(ctx); err != nil {
		log.Fatal("generator failed: ", err)
	}
	log.Println("Generator completed.")
}
