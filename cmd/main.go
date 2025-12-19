package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"syscall"

	"github.com/dioptra-io/retina-generator/internal/generator"
)

func main() {
	address := flag.String("address", ":50050", "Address of the orchestrator to connect")
	seed := flag.Int64("seed", 42, "Seed for the random generator")
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := generator.Run(ctx, *address, *seed); err != nil && err != ctx.Err() {
		log.Fatal(err)
	}
}
