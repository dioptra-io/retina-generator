// Copyright (c) 2026 Sorbonne Université
// SPDX-License-Identifier: MIT
//
// Package generator implements the retina-generator, which generates Probing Directives (PDs)
// and writes them to a JSONL file.
package generator

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"net"
	"os"
	"strings"
	"time"

	"github.com/dioptra-io/retina-commons/api/v1"
)

const maxIPGenerationAttempts = 100

// Config defines the parameters used to generate and write PDs.
type Config struct {
	Seed          int64
	MinTTL        uint8
	MaxTTL        uint8
	AgentIDs      []string
	NumPDs        uint64
	OutputFile    string
	BlocklistFile string
}

// Gen generates ProbingDirectives and writes them to a JSONL file.
type Gen struct {
	config    *Config
	logger    *slog.Logger
	blocklist []*net.IPNet
}

// NewGen validates the provided Config and returns a new generator.
// It returns an error if the configuration is invalid.
func NewGen(config *Config, logger *slog.Logger) (*Gen, error) {
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

	var blocklist []*net.IPNet
	if config.BlocklistFile != "" {
		var err error
		blocklist, err = parseBlocklist(config.BlocklistFile)
		if err != nil {
			return nil, fmt.Errorf("failed to parse blocklist file: %w", err)
		}
		logger.Info("Blocklist loaded", slog.Int("networks", len(blocklist)))
	}

	return &Gen{
		config:    config,
		logger:    logger,
		blocklist: blocklist,
	}, nil
}

// Run generates Probing Directives and writes them to the configured JSONL file.
// It respects context cancellation between iterations.
func (g *Gen) Run(ctx context.Context) error {
	start := time.Now()
	g.logger.Info("Starting PD generation",
		slog.Uint64("num_pds", g.config.NumPDs),
		slog.String("output_file", g.config.OutputFile),
		slog.Int("agent_count", len(g.config.AgentIDs)),
	)

	pds := make([]*api.ProbingDirective, 0, g.config.NumPDs)
	random := rand.New(rand.NewSource(g.config.Seed)) //nolint:gosec // G404: intentional, seeded deterministic generator
	for i := uint64(0); i < g.config.NumPDs; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		pd, err := generatePD(
			random,
			i,
			g.config.AgentIDs,
			g.config.MinTTL,
			g.config.MaxTTL,
			g.blocklist)
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
		slog.Int("skipped", int(g.config.NumPDs)-len(pds)), //nolint:gosec // G115: safe on 64-bit platforms; int and uint64 are both 64-bit
		slog.String("output_file", g.config.OutputFile),
		slog.Int64("seed", g.config.Seed),
		slog.Float64("duration_seconds", time.Since(start).Seconds()),
	)
	return nil
}

// writePDsToFile writes each ProbingDirective as a JSON object on its own line
// (JSONL format) to the file at the given path, creating or truncating it.
func writePDsToFile(pds []*api.ProbingDirective, path string) error {
	f, err := os.Create(path) //nolint:gosec // G304: path is caller-provided by design
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

func generatePD(random *rand.Rand, id uint64, agentIDs []string, minTTL, maxTTL uint8, blocklist []*net.IPNet) (*api.ProbingDirective, error) {
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
		if isBlocked(candidateAddress, blocklist) {
			continue
		}
		destinationAddress = candidateAddress
		break
	}
	if destinationAddress == nil {
		return nil, fmt.Errorf("failed to generate routable IP address after %d attempts", maxIPGenerationAttempts)
	}

	nearTTL := minTTL + uint8(random.Intn(int(maxTTL-minTTL+1))) //nolint:gosec // G115: value bounded by maxTTL-minTTL+1 which fits in uint8

	return &api.ProbingDirective{
		ProbingDirectiveID: id,
		IPVersion:          ipVersion,
		Protocol:           protocol,
		AgentID:            agentID,
		DestinationAddress: destinationAddress,
		NearTTL:            nearTTL,
		NextHeader:         buildNextHeader(random, protocol),
	}, nil
}

// buildNextHeader constructs the protocol-specific NextHeader for a ProbingDirective.
func buildNextHeader(random *rand.Rand, protocol api.Protocol) api.NextHeader {
	switch protocol {
	case api.ICMP:
		return api.NextHeader{
			ICMPNextHeader: &api.ICMPNextHeader{
				FirstHalfWord:  uint16(random.Intn(1 << 16)), //nolint:gosec // G115: value bounded by 1<<16
				SecondHalfWord: uint16(random.Intn(1 << 16)), //nolint:gosec // G115: value bounded by 1<<16
			},
		}
	case api.UDP:
		return api.NextHeader{
			UDPNextHeader: &api.UDPNextHeader{
				SourcePort:      uint16(random.Intn(1 << 16)), //nolint:gosec // G115: value bounded by 1<<16
				DestinationPort: uint16(random.Intn(1 << 16)), //nolint:gosec // G115: value bounded by 1<<16
			},
		}
	case api.ICMPv6:
		return api.NextHeader{
			ICMPv6NextHeader: &api.ICMPv6NextHeader{
				FirstHalfWord:  uint16(random.Intn(1 << 16)), //nolint:gosec // G115: value bounded by 1<<16
				SecondHalfWord: uint16(random.Intn(1 << 16)), //nolint:gosec // G115: value bounded by 1<<16
			},
		}
	default:
		panic(fmt.Sprintf("buildNextHeader: unsupported protocol %v", protocol))
	}
}

func generateAddress(random *rand.Rand, ipVersion api.IPVersion) (net.IP, error) {
	var length int
	switch ipVersion {
	case api.IPv4:
		length = net.IPv4len
	case api.IPv6:
		length = net.IPv6len
	default:
		// Unreachable: ipVersion is always IPv4 or IPv6, chosen from a fixed
		// slice in generatePD. Kept as a defensive check for future callers.
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

func parseBlocklist(path string) ([]*net.IPNet, error) {
	f, err := os.Open(path) //nolint:gosec // G304: path is caller-provided by design
	if err != nil {
		return nil, fmt.Errorf("cannot open blocklist file: %w", err)
	}
	defer func() { _ = f.Close() }()

	var blocklist []*net.IPNet
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		_, ipNet, err := net.ParseCIDR(line)
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR %q: %w", line, err)
		}
		blocklist = append(blocklist, ipNet)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading blocklist file: %w", err)
	}
	return blocklist, nil
}

func isBlocked(ip net.IP, blocklist []*net.IPNet) bool {
	for _, network := range blocklist {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}
