package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"log"
	"math"
	"net"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	var (
		udsPath              = flag.String("uds_path", "", "Path of the Unix Domain Socket used to connect to the orchestrator.")
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
		UDSPath:            *udsPath,
		Seed:               *seed,
		MinTTL:             uint8(*minTTL),
		MaxTTL:             uint8(*maxTTL),
		MaxAddressGenTries: *maxAddressGenRetries,
	}

	// Get the stdio or dial unix socket connection.
	rw, closer, err := getConn(*udsPath)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := closer.Close(); err != nil {
			log.Fatal("Closing failed: ", err)
		}
	}()
	cfg.ReadWriter = rw

	// Run the generator until completion or context cancellation.
	// Ignore the context cancellation error, as it represents an expected shutdown path.
	if err := RunGenerator(ctx, cfg); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, io.EOF) {
		log.Fatal("Generator failed: ", err)
	}
}

func getConn(udsPath string) (io.ReadWriter, io.Closer, error) {
	if udsPath == "" {
		conn := struct {
			io.Reader
			io.Writer
		}{
			Reader: os.Stdin,
			Writer: os.Stdout,
		}

		// no-op closer: never close stdio
		return conn, io.NopCloser(nil), nil
	}

	conn, err := net.Dial("unix", udsPath)
	if err != nil {
		return nil, nil, err
	}
	return conn, conn, nil
}
