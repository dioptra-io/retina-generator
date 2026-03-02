// Copyright (c) 2025 Dioptra
// SPDX-License-Identifier: MIT

// Package retina implements the retina-generator, which generates Probing Directives (PDs)
// and sends them to retina-orchestrator.
package retina

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/dioptra-io/retina-commons/api/v1"
)

const maxIPGenerationAttempts = 100

// Config defines the parameters used to generate and submit PDs.
type Config struct {
	Seed     int64
	MinTTL   uint8
	MaxTTL   uint8
	AgentIDs []string
	NumPDs   uint64
	// OrchestratorAddress is the full URL of the orchestrator (e.g. http://localhost:8080).
	OrchestratorAddress string
	// HTTPTimeout is the timeout value for the request. Zero means no timeout.
	HTTPTimeout time.Duration
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
	if config.OrchestratorAddress == "" {
		return nil, fmt.Errorf("orchestrator address cannot be empty")
	}
	if config.HTTPTimeout < 0 {
		return nil, fmt.Errorf("HTTP timeout cannot be negative")
	}

	return &gen{
		config: config,
	}, nil
}

// Run generates Probing Directives and sends them to the orchestrator.
func (g *gen) Run(ctx context.Context) error {
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
		// TODO: replace with structured debug logging
		log.Printf("Generated PD %d: %+v", i, pd)
		pds = append(pds, pd)
	}

	url := fmt.Sprintf("%s/directives", strings.TrimRight(g.config.OrchestratorAddress, "/"))

	return sendPDs(ctx, pds, url, g.config.HTTPTimeout)
}

func sendPDs(ctx context.Context, pds []*api.ProbingDirective, url string, timeout time.Duration) error {
	body, err := json.Marshal(pds)
	if err != nil {
		return fmt.Errorf("marshal PDs: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusBadRequest:
		return fmt.Errorf("orchestrator returned 400 (bad request)")
	case http.StatusInternalServerError:
		return fmt.Errorf("orchestrator returned 500 (internal server error)")
	default:
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}
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
		if !isRoutable(candidateAddress) {
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

	// TODO: replace with structured debug logging
	log.Printf("Generated PD %d: %+v", id, pd)

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

func isRoutable(ip net.IP) bool {
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
