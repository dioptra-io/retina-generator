// Copyright (c) 2025 Dioptra
// SPDX-License-Identifier: MIT

// retina-generator generates Probing Directives (PDs) and writes them to a JSONL file.
package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"syscall"

	"github.com/dioptra-io/retina-generator/internal/retina"
)

func main() {
	flag.Usage = func() {
		log.Printf("Usage: retina-generator [flags] agentID1 agentID2 ...\n")
		flag.PrintDefaults()
	}

	var (
		seed       = flag.Int64("seed", 42, "Seed for the random generator.")
		minTTL     = flag.Uint("min-ttl", 1, "Minimum TTL value for generated PDs (0-255).")
		maxTTL     = flag.Uint("max-ttl", 32, "Maximum TTL value for generated PDs (0-255).")
		numPDs     = flag.Uint64("num-pds", 100, "Number of Probing Directives to generate.")
		outputFile = flag.String("output-file", "", "Path to the output file where PDs will be written as JSONL.")
	)

	flag.Parse()

	agentIDs := flag.Args()

	if *minTTL > 255 || *maxTTL > 255 {
		log.Fatal("min-ttl and max-ttl must be between 0 and 255")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	gen, err := retina.NewGen(&retina.Config{
		Seed:       *seed,
		MinTTL:     uint8(*minTTL),
		MaxTTL:     uint8(*maxTTL),
		AgentIDs:   agentIDs,
		NumPDs:     *numPDs,
		OutputFile: *outputFile,
	})
	if err != nil {
		log.Fatalf("Cannot create generator with the provided config: %v", err)
	}

	if err := gen.Run(ctx); err != nil {
		log.Fatalf("Failed to write directives: %v", err)
	}
	log.Printf("Successfully wrote %d directives to %s", *numPDs, *outputFile)
}
