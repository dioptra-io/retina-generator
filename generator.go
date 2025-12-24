// Copyright (c) 2025 Dioptra
// SPDX-License-Identifier: MIT
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log"
	"math/rand"
	"net"
	"sync"
	"time"

	"github.com/dioptra-io/retina-commons/pkg/api/v1"
	"golang.org/x/sync/errgroup"
)

// Config contains the configuration parameters for the PD generation.
type Config struct {
	// UDSPath is the path of the Unix Domain Socket for orchestrator-generator communication.
	UDSPath string

	// Seed is the seed used by the random generator.
	Seed int64

	// MinTTL is the minimum possible TTL value for the generated PD.
	MinTTL uint8

	// MaxTTL is the maximum possible TTL value for the generated PD.
	MaxTTL uint8

	// DefaultProbingImpactLimit is the default value of ProbingImpactLimit in status.
	DefaultProbingImpactLimit time.Duration
}

// generator continuously generates PDs and sends them to a Unix domain socket to be read by the orchestrator. In
// parallel, it reads SS structs from the socket to update the system status.
type generator struct {
	// config holds generator-specific parameters.
	config Config

	// status is the current system status sent by the orchestrator.
	status *api.SystemStatus

	// random is the PRNG used for PD generation.
	random *rand.Rand

	// notifyChan is used to wake up the generator routine.
	notifyChan chan struct{}

	// mutex protects access to status.
	mutex sync.Mutex
}

func RunGenerator(parentCtx context.Context, config *Config) error {
	generator := &generator{
		config: *config,
		status: &api.SystemStatus{
			ProbingImpactLimitMS:           uint(config.DefaultProbingImpactLimit),
			DisallowedDestinationAddresses: []net.IP{},
			ActiveAgentIDs:                 []string{},
		},
		notifyChan: make(chan struct{}, 1),
		random:     rand.New(rand.NewSource(config.Seed)),
	}

	conn, err := net.Dial("unix", config.UDSPath)
	if err != nil {
		return err
	}
	defer func() {
		close(generator.notifyChan)
		if err := conn.Close(); err != nil {
			log.Printf("Cannot close connection; %v", err)
		}
	}()

	encoder := json.NewEncoder(bufio.NewWriter(conn))
	decoder := json.NewDecoder(bufio.NewReader(conn))

	log.Printf("Generator connected to socket %q", config.UDSPath)

	group, ctx := errgroup.WithContext(parentCtx)

	// Goroutine: receive SS struct, update local state, notify generator.
	group.Go(func() error {
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()

			default:
				var status api.SystemStatus
				if err := decoder.Decode(&status); err != nil {
					return err
				}
				generator.updateStatus(&status)
				generator.notify()
			}
		}
	})

	// Goroutine: wait until there are active agents, generate PD struct, send it to the orchestrator.
	group.Go(func() error {
		var (
			pd  *api.ProbingDirective
			ok  bool
			err error
		)

		for {
			if err := generator.wait(ctx); err != nil {
				return err
			}
			if pd, ok = generator.generatePD(); !ok {
				continue
			}
			if err = encoder.Encode(pd); err != nil {
				return err
			}
		}
	})

	// Wait until both loops end.
	if err := group.Wait(); err != nil && err != ctx.Err() && err != io.EOF {
		return err
	}

	log.Println("Generator stopped.")

	return nil
}

// updateStatus updates the existing status with the given status.
func (g *generator) updateStatus(newStatus *api.SystemStatus) {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	g.status = newStatus
}

// notify tries to push to the notifyChan for waking up the generator routine.
func (g *generator) notify() {
	// Push notify channel if possible.
	select {
	case g.notifyChan <- struct{}{}:

	default:
	}
}

// wait blocks the current goroutine until either notifyChan is signaled and status.ActiveAgentIDs becomes non-empty, or
// the context is canceled.
//
// It returns ctx.Err() if the context is canceled.
func (g *generator) wait(ctx context.Context) error {
	for {
		g.mutex.Lock()
		if len(g.status.ActiveAgentIDs) > 0 {
			g.mutex.Unlock()
			break
		}
		g.mutex.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-g.notifyChan:
		}
	}
	return nil
}

// generatePD generates a new PD struct pseudo-randomly using the given seed, status, and config.
func (g *generator) generatePD() (*api.ProbingDirective, bool) {
	g.mutex.Lock()
	defer g.mutex.Unlock()

	// Randomize AgentID
	numActiveAgents := len(g.status.ActiveAgentIDs)
	if numActiveAgents == 0 {
		return nil, false
	}
	agentID := g.status.ActiveAgentIDs[g.random.Intn(numActiveAgents)]

	// Randomize IPVersion
	ipVersion := []api.IPVersion{
		api.TypeIPv4,
		api.TypeIPv6,
	}[g.random.Intn(2)]

	// Randomize Protocol
	var protocol api.Protocol
	if ipVersion == api.TypeIPv4 {
		protocol = []api.Protocol{
			api.UDP,
			api.ICMP,
		}[g.random.Intn(2)]
	} else {
		protocol = []api.Protocol{
			api.UDP,
			api.ICMPv6,
		}[g.random.Intn(2)]
	}

	// Randomize DestinationAddress
	destinationAddress, ok := generatePublicAddress(g.random, ipVersion, g.status.DisallowedDestinationAddresses)
	if !ok {
		return nil, false
	}

	// Randomize NearTTL
	nearTTL := g.config.MinTTL + uint8(rand.Intn(int(g.config.MaxTTL-g.config.MinTTL+1)))

	// Randomize NextHeader
	nextHeader := api.NextHeader{}
	switch protocol {
	case api.ICMP:
		nextHeader.ICMPNextHeader = &api.ICMPNextHeader{
			FirstHalfWord:  uint16(g.random.Intn(1 << 16)),
			SecondHalfWord: uint16(g.random.Intn(1 << 16)),
		}
	case api.UDP:
		nextHeader.UDPNextHeader = &api.UDPNextHeader{
			SourcePort:      uint16(g.random.Intn(1 << 16)),
			DestinationPort: uint16(g.random.Intn(1 << 16)),
		}
	case api.ICMPv6:
		nextHeader.ICMPv6NextHeader = &api.ICMPv6NextHeader{
			FirstHalfWord:  uint16(g.random.Intn(1 << 16)),
			SecondHalfWord: uint16(g.random.Intn(1 << 16)),
		}
	}

	return &api.ProbingDirective{
		IPVersion:          ipVersion,
		Protocol:           protocol,
		AgentID:            agentID,
		DestinationAddress: destinationAddress,
		NearTTL:            nearTTL,
		NextHeader:         nextHeader,
	}, true
}

// generatePublicAddress generates a random public IP address from the given random that is not in the disallowedIPs.
//
// It returns (nil, false) if it cannot generate after maxAttempts.
func generatePublicAddress(random *rand.Rand, ipVersion api.IPVersion, disallowedIPs []net.IP) (net.IP, bool) {
	const maxAttempts = 10_000

	// Build deny-list for O(1) lookups
	deny := make(map[[16]byte]struct{}, len(disallowedIPs))
	for _, ip := range disallowedIPs {
		// Converts to 16 bit representation since IPv6 supersets IPv4.
		var k [16]byte
		copy(k[:], ip.To16())
		deny[k] = struct{}{}
	}

	for range maxAttempts {
		var ip net.IP

		switch ipVersion {
		case api.TypeIPv4:
			ip = net.IPv4(
				byte(random.Intn(256)),
				byte(random.Intn(256)),
				byte(random.Intn(256)),
				byte(random.Intn(256)),
			)

		case api.TypeIPv6:
			ip = make(net.IP, net.IPv6len)
			_, _ = random.Read(ip) // math/rand never returns an error
		}

		if !isPublicIP(ip) {
			continue
		}

		var k [16]byte
		copy(k[:], ip.To16())
		if _, blocked := deny[k]; blocked {
			continue
		}

		return ip, true
	}

	// Extremely unlikely
	return nil, false
}

func isPublicIP(ip net.IP) bool {
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
