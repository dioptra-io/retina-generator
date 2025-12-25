package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"syscall"
)

func main() {
	var (
		udsPath              = flag.String("uds_path", "/tmp/retina.sock", "Path of the Unix Domain Socket used to connect to the orchestrator.")
		seed                 = flag.Int64("seed", 42, "Seed for the random generator.")
		minTTL               = flag.Uint("min_ttl", 1, "Minimum TTL value for generated PDs.")
		maxTTL               = flag.Uint("max_ttl", 32, "Maximum TTL value for generated PDs.")
		maxAddressGenRetries = flag.Uint("max_address_gen_retries", 1_000, "Maximum number of retries for address generation.")
	)

	flag.Parse()

	// Create a root context that is canceled on SIGINT or SIGTERM.
	// This allows the generator to shut down gracefully.
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Build the runtime configuration from parsed flags.
	cfg := Config{
		UDSPath:            *udsPath,
		Seed:               *seed,
		MinTTL:             uint8(*minTTL),
		MaxTTL:             uint8(*maxTTL),
		MaxAddressGenTries: *maxAddressGenRetries,
	}

	// Run the generator until completion or context cancellation.
	// Ignore the context cancellation error, as it represents an expected shutdown path.
	if err := RunGenerator(ctx, cfg); err != nil && err != ctx.Err() {
		log.Fatal(err)
	}
}
