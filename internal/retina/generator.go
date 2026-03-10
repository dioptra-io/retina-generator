// Copyright (c) 2025 Dioptra
// SPDX-License-Identifier: MIT

// Package retina implements the retina-generator, which generates Probing Directives (PDs)
// and writes them to a JSONL file.
package retina

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net"
	"os"

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
}

// NewGen validates the provided Config and returns a new generator.
// It returns an error if the configuration is invalid.
func NewGen(config *Config) (*gen, error) {
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
	}, nil
}

// Run generates Probing Directives and writes them to the configured JSONL file.
func (g *gen) Run(_ context.Context) error {
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
			log.Printf("Failed to generate PD %d: %v", i, err)
			continue
		}
		pds = append(pds, pd)
	}

	return writePDsToFile(pds, g.config.OutputFile)
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
