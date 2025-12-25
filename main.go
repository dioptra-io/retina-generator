package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"log"
	"math"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	var (
		seed                 = flag.Int64("seed", 42, "Seed for the random generator.")
		minTTL               = flag.Uint("min_ttl", 1, "Minimum TTL value for generated PDs.")
		maxTTL               = flag.Uint("max_ttl", 32, "Maximum TTL value for generated PDs.")
		maxAddressGenRetries = flag.Uint("max_address_gen_retries", 1_000, "Maximum number of retries for address generation.")
	)

	flag.Parse()

	if *minTTL > math.MaxUint8 || *maxTTL > math.MaxUint8 {
		log.Fatal("min_ttl and max_ttl must be <= 255")
	}

	// Create a root context that is canceled on SIGINT or SIGTERM.
	// This allows the generator to shut down gracefully.
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cfg := Config{
		Seed:               *seed,
		MinTTL:             uint8(*minTTL),
		MaxTTL:             uint8(*maxTTL),
		MaxAddressGenTries: *maxAddressGenRetries,
		Reader:             os.Stdin,
		Writer:             os.Stdout,
	}

	// Run the generator until completion or context cancellation.
	// Ignore the context cancellation error, as it represents an expected shutdown path.
	if err := RunGenerator(ctx, cfg); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, io.EOF) {
		log.Fatal("Generator failed: ", err)
	}
}
