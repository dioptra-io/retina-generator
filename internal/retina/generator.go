// Copyright (c) 2025 Dioptra
// SPDX-License-Identifier: MIT

// Package retina implements the retina-generator, which generates Probing Directives (PDs)
// and writes them to a JSONL file.
package retina

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"net"
	"os"
	"time"

	"github.com/dioptra-io/retina-commons/api/v1"
)

const maxIPGenerationAttempts = 100

// Config defines the parameters used to generate and write PDs.
type Config struct {
	Seed       int64
	MinTTL     uint8
	MaxTTL     uint8
	AgentIDs   []string
	NumPDs     uint64
	OutputFile string
}

type gen struct {
	config *Config
	logger *slog.Logger
}

// NewGen validates the provided Config and returns a new generator.
// It returns an error if the configuration is invalid.
func NewGen(config *Config, logger *slog.Logger) (*gen, error) {
	if len(config.AgentIDs) == 0 {
		return nil, fmt.Errorf("agentIDs cannot be empty")
	}
	if config.MinTTL > config.MaxTTL {
		return nil, fmt.Errorf("min TTL cannot be greater than max TTL")
	}
	if config.NumPDs == 0 {
		return nil, fmt.Errorf("number of PDs cannot be zero")
	}
	if config.OutputFile == "" {
		return nil, fmt.Errorf("output file cannot be empty")
	}

	return &gen{
		config: config,
		logger: logger,
	}, nil
}

// Run generates Probing Directives and writes them to the configured JSONL file.
func (g *gen) Run(_ context.Context) error {
	start := time.Now()
	g.logger.Info("Starting PD generation",
		slog.Uint64("num_pds", g.config.NumPDs),
		slog.String("output_file", g.config.OutputFile),
		slog.Int("agent_count", len(g.config.AgentIDs)),
	)

	pds := make([]*api.ProbingDirective, 0, g.config.NumPDs)
	random := rand.New(rand.NewSource(g.config.Seed))
	for i := uint64(0); i < g.config.NumPDs; i++ {
		pd, err := generatePD(
			random,
			i,
			g.config.AgentIDs,
			g.config.MinTTL,
			g.config.MaxTTL)
		if err != nil {
			g.logger.Warn("Skipping PD: generation failed",
				slog.Uint64("pd_index", i),
				slog.String("reason", err.Error()),
			)
			continue
		}
		pds = append(pds, pd)
	}

	if err := writePDsToFile(pds, g.config.OutputFile); err != nil {
		g.logger.Error("Failed to write PDs to file",
			slog.Int("generated", len(pds)),
			slog.String("output_file", g.config.OutputFile),
			slog.Any("err", err),
		)
		return err
	}

	g.logger.Info("PD generation complete",
		slog.Int("written", len(pds)),
		slog.Uint64("requested", g.config.NumPDs),
		slog.Int("skipped", int(g.config.NumPDs)-len(pds)),
		slog.String("output_file", g.config.OutputFile),
		slog.Int64("seed", g.config.Seed),
		slog.Float64("duration_seconds", time.Since(start).Seconds()),
	)
	return nil
}

// writePDsToFile writes each ProbingDirective as a JSON object on its own line
// (JSONL format) to the file at the given path, creating or truncating it.
func writePDsToFile(pds []*api.ProbingDirective, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("open output file: %w", err)
	}
	defer func() {
		_ = f.Close()
	}()

	enc := json.NewEncoder(f)
	for _, pd := range pds {
		if err := enc.Encode(pd); err != nil {
			return fmt.Errorf("encode PD %d: %w", pd.ProbingDirectiveID, err)
		}
	}
	return nil
}

func generatePD(random *rand.Rand, id uint64, agentIDs []string, minTTL, maxTTL uint8) (*api.ProbingDirective, error) {
	agentID := agentIDs[random.Intn(len(agentIDs))]

	ipVersion := []api.IPVersion{
		api.IPv4,
		api.IPv6,
	}[random.Intn(2)]

	var protocol api.Protocol
	if ipVersion == api.IPv4 {
		protocol = []api.Protocol{
			api.UDP,
			api.ICMP,
		}[random.Intn(2)]
	} else {
		protocol = []api.Protocol{
			api.UDP,
			api.ICMPv6,
		}[random.Intn(2)]
	}

	var destinationAddress net.IP
	for range maxIPGenerationAttempts {
		candidateAddress, err := generateAddress(random, ipVersion)
		if err != nil {
			return nil, fmt.Errorf("cannot generate address: %w", err)
		}
		if !isPublic(candidateAddress) {
			continue
		}
		destinationAddress = candidateAddress
		break
	}
	if destinationAddress == nil {
		return nil, fmt.Errorf("failed to generate routable IP address after %d attempts", maxIPGenerationAttempts)
	}

	nearTTL := minTTL + uint8(random.Intn(int(maxTTL-minTTL+1)))

	nextHeader := api.NextHeader{}
	switch protocol {
	case api.ICMP:
		nextHeader.ICMPNextHeader = &api.ICMPNextHeader{
			FirstHalfWord:  uint16(random.Intn(1 << 16)),
			SecondHalfWord: uint16(random.Intn(1 << 16)),
		}
	case api.UDP:
		nextHeader.UDPNextHeader = &api.UDPNextHeader{
			SourcePort:      uint16(random.Intn(1 << 16)),
			DestinationPort: uint16(random.Intn(1 << 16)),
		}
	case api.ICMPv6:
		nextHeader.ICMPv6NextHeader = &api.ICMPv6NextHeader{
			FirstHalfWord:  uint16(random.Intn(1 << 16)),
			SecondHalfWord: uint16(random.Intn(1 << 16)),
		}
	}

	pd := &api.ProbingDirective{
		ProbingDirectiveID: id,
		IPVersion:          ipVersion,
		Protocol:           protocol,
		AgentID:            agentID,
		DestinationAddress: destinationAddress,
		NearTTL:            nearTTL,
		NextHeader:         nextHeader,
	}

	return pd, nil
}

func generateAddress(random *rand.Rand, ipVersion api.IPVersion) (net.IP, error) {
	var length int
	switch ipVersion {
	case api.IPv4:
		length = net.IPv4len
	case api.IPv6:
		length = net.IPv6len
	default:
		return nil, fmt.Errorf("invalid IP version: expected 4 or 6, got %v", ipVersion)
	}

	ip := make(net.IP, length)
	_, _ = random.Read(ip)
	return ip, nil
}

// isPublic reports whether ip is a publicly addressable, non-multicast address.
func isPublic(ip net.IP) bool {
	if ip == nil {
		return false
	}
	return !ip.IsUnspecified() &&
		!ip.IsLoopback() &&
		!ip.IsPrivate() &&
		!ip.IsLinkLocalUnicast() &&
		!ip.IsLinkLocalMulticast() &&
		!ip.IsMulticast()
}
