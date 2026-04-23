// Copyright (c) 2026 Sorbonne Université
// SPDX-License-Identifier: MIT

// retina-generator generates Probing Directives (PDs) and writes them to a JSONL file.
package main

import (
	"context"
	"flag"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/dioptra-io/retina-generator/internal/generator"
)

func main() {
	flag.Usage = func() {
		log.Printf("Usage: retina-generator [flags] agentID1 agentID2 ...\n")
		flag.PrintDefaults()
	}

	var (
		seed          = flag.Int64("seed", 42, "Seed for the random generator.")
		minTTL        = flag.Uint("min-ttl", 1, "Minimum TTL value for generated PDs (0-255).")
		maxTTL        = flag.Uint("max-ttl", 32, "Maximum TTL value for generated PDs (0-255).")
		numPDs        = flag.Uint64("num-pds", 100, "Number of Probing Directives to generate.")
		outputFile    = flag.String("output-file", "", "Path to the output file where PDs will be written as JSONL.")
		blocklistFile = flag.String("blocklist-file", "", "Path to a file containing CIDR networks to block (one per line).")
		logLevel      = flag.String("log-level", "info", "Log level (debug, info, warn, error).")
	)
	flag.Parse()

	agentIDs := flag.Args()

	if *minTTL > 255 || *maxTTL > 255 {
		log.Fatal("min-ttl and max-ttl must be between 0 and 255")
	}

	var level slog.Level
	if err := level.UnmarshalText([]byte(*logLevel)); err != nil {
		log.Fatalf("Invalid log level %q (want debug, info, warn, or error): %v", *logLevel, err)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	}))

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)

	gen, err := generator.NewGen(&generator.Config{
		Seed:          *seed,
		MinTTL:        uint8(*minTTL), //nolint:gosec // G115: bounds checked above
		MaxTTL:        uint8(*maxTTL), //nolint:gosec // G115: bounds checked above
		AgentIDs:      agentIDs,
		NumPDs:        *numPDs,
		OutputFile:    *outputFile,
		BlocklistFile: *blocklistFile,
	}, logger)
	if err != nil {
		cancel()
		logger.Error("Cannot create generator with the provided config", slog.Any("err", err))
		os.Exit(1)
	}

	if err := gen.Run(ctx); err != nil {
		cancel()
		logger.Error("Failed to write directives", slog.Any("err", err))
		os.Exit(1)
	}

	cancel()
}
