// Copyright (c) 2025 Dioptra
// SPDX-License-Identifier: MIT
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"sync"

	"github.com/dioptra-io/retina-commons/pkg/api/v1"
	"golang.org/x/sync/errgroup"
)

// Config contains the configuration parameters for the PD generation.
type Config struct {
	// Seed is the seed used by the random generator.
	Seed int64

	// MinTTL is the minimum possible TTL value for the generated PD.
	MinTTL uint8

	// MaxTTL is the maximum possible TTL value for the generated PD.
	MaxTTL uint8

	// MaxAddressGenTries is the maximum number of attempts to generate a value that satisfies the acceptance criteria.
	// For example; generated destination address might be in the disallowed IP list, in that case retry until
	// MaxAddressGenTries times.
	MaxAddressGenTries uint

	// Reader is the stream that the SS structs are decoded from.
	Reader io.Reader

	// Writer is the stream that the generated PDs are encoded to.
	Writer io.Writer
}

// state represents the internal state of the generator.
type state struct {
	// disallowedDestinations is a set of IP addresses that generator is not allowed to generate as destinationAddress.
	disallowedDestinations map[[16]byte]struct{}

	// activeAgentIDs is a set of agentIDs that can be used on PD generation.
	activeAgentIDs []string

	// random is a non-cryptographic PRNG used for probe generation.
	random *rand.Rand
}

// generator continuously generates PDs and sends them to a Unix domain socket to be read by the orchestrator.
// In parallel, it reads SS structs from the socket to update the system status.
type generator struct {
	// state is the internal generator state, it contains values from config and orchestrator.
	// Expected to mutate thus protected by the mutex.
	state state

	// config holds generator-specific parameters.
	// Expected to not mutate.
	config Config

	// notifyChan signals that the system status has changed.
	// When there are no active agents, the generation logic blocks until it is
	// woken up by a notification on this channel.
	notifyChan chan struct{}

	// mutex protects access to state.
	mutex sync.Mutex
}

// RunGenerator creates a generator, connects to the orchestrator, and starts PD generation.
// In case of updates, it changes its internal state.
// It blocks until the context is cancelled, or an error occurs.
func RunGenerator(parentCtx context.Context, config Config) error {
	generator := &generator{
		state: state{
			disallowedDestinations: make(map[[16]byte]struct{}),
			random:                 rand.New(rand.NewSource(config.Seed)),
			activeAgentIDs:         []string{},
		},
		config: config,
		// notifyChan is a semaphore to wake up the goroutine that generates PD.
		notifyChan: make(chan struct{}, 1),
	}
	defer close(generator.notifyChan)

	group, ctx := errgroup.WithContext(parentCtx)

	// Goroutine: receive SS struct, update local state, notify generator if necessary.
	group.Go(func() error {
		var (
			decoder = json.NewDecoder(generator.config.Reader)
			status  api.SystemStatus
			err     error
		)

		for {
			if err = decoder.Decode(&status); err != nil {
				return fmt.Errorf("cannot decode status: %w", err)
			}
			generator.updateStatus(&status)
			if err = generator.notify(ctx); err != nil {
				return fmt.Errorf("cannot notify: %w", err)
			}
		}
	})

	// Goroutine: wait until there are active agents, generate PD struct, encode & send it to the orchestrator.
	group.Go(func() error {
		var (
			encoder = json.NewEncoder(generator.config.Writer)
			pd      *api.ProbingDirective
			ok      bool
			err     error
		)

		for {
			if err := generator.wait(ctx); err != nil {
				return fmt.Errorf("cannot wait: %w", err)
			}
			if pd, ok = generator.generatePD(); !ok {
				continue
			}
			if err = encoder.Encode(pd); err != nil {
				return fmt.Errorf("cannot encode PD: %w", err)
			}
		}
	})

	// Wait until both loops end.
	if err := group.Wait(); err != nil && err != ctx.Err() {
		return fmt.Errorf("generator failed: %w", err)
	}

	log.Println("Generator stopped.")

	return nil
}

// updateStatus updates the existing status with the given SS struct.
func (g *generator) updateStatus(newStatus *api.SystemStatus) {
	g.mutex.Lock()
	defer g.mutex.Unlock()

	g.state.activeAgentIDs = removeDuplicates(newStatus.ActiveAgentIDs)
	g.state.disallowedDestinations = toIPMap(newStatus.DisallowedDestinationAddresses)
}

// notify blocks until a notification is pushed to the notifyChan for waking up the generator routine, or context is
// cancelled.
func (g *generator) notify(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()

	case g.notifyChan <- struct{}{}:
		return nil
	}
}

// wait blocks the current goroutine until either notifyChan is signaled and status.ActiveAgentIDs becomes non-empty, or
// the context is canceled.
//
// It returns ctx.Err() if the context is canceled.
func (g *generator) wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()

	default:
	}

	for {
		g.mutex.Lock()
		if len(g.state.activeAgentIDs) > 0 {
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
//
// It returns the (nil, false) if the generation fails.
func (g *generator) generatePD() (*api.ProbingDirective, bool) {
	// Holding the lock here because the this function uses elements from the internal state.
	g.mutex.Lock()
	defer g.mutex.Unlock()

	// Randomize AgentID
	numActiveAgents := len(g.state.activeAgentIDs)
	if numActiveAgents == 0 {
		return nil, false
	}
	agentID := g.state.activeAgentIDs[g.state.random.Intn(numActiveAgents)]

	// Randomize IPVersion
	ipVersion := []api.IPVersion{
		api.TypeIPv4,
		api.TypeIPv6,
	}[g.state.random.Intn(2)]

	// Randomize Protocol
	var protocol api.Protocol
	if ipVersion == api.TypeIPv4 {
		protocol = []api.Protocol{
			api.UDP,
			api.ICMP,
		}[g.state.random.Intn(2)]
	} else {
		protocol = []api.Protocol{
			api.UDP,
			api.ICMPv6,
		}[g.state.random.Intn(2)]
	}

	// Randomize DestinationAddress
	var destinationAddress net.IP
	for range g.config.MaxAddressGenTries {
		ip, ok := generateAddress(g.state.random, ipVersion)
		if !ok {
			return nil, false
		}
		if !isPublicIP(ip) {
			continue
		}
		if _, ok := g.state.disallowedDestinations[ipToBytes16(ip)]; ok {
			continue
		}

		destinationAddress = ip
		break
	}
	if destinationAddress == nil {
		return nil, false
	}

	// Randomize NearTTL
	nearTTL := g.config.MinTTL + uint8(g.state.random.Intn(int(g.config.MaxTTL-g.config.MinTTL+1)))

	// Randomize NextHeader
	nextHeader := api.NextHeader{}
	switch protocol {
	case api.ICMP:
		nextHeader.ICMPNextHeader = &api.ICMPNextHeader{
			FirstHalfWord:  uint16(g.state.random.Intn(1 << 16)),
			SecondHalfWord: uint16(g.state.random.Intn(1 << 16)),
		}
	case api.UDP:
		nextHeader.UDPNextHeader = &api.UDPNextHeader{
			SourcePort:      uint16(g.state.random.Intn(1 << 16)),
			DestinationPort: uint16(g.state.random.Intn(1 << 16)),
		}
	case api.ICMPv6:
		nextHeader.ICMPv6NextHeader = &api.ICMPv6NextHeader{
			FirstHalfWord:  uint16(g.state.random.Intn(1 << 16)),
			SecondHalfWord: uint16(g.state.random.Intn(1 << 16)),
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

// Standalone functions

// generateAddress generates a random public IP address for the given IP version.
//
// It returns (nil, false) if the given ipVersion is not IPv4 or IPv6.
func generateAddress(random *rand.Rand, ipVersion api.IPVersion) (net.IP, bool) {
	var ip net.IP

	switch ipVersion {
	case api.TypeIPv4:
		ip = make(net.IP, net.IPv4len)
		_, _ = random.Read(ip) // math/rand never returns an error

	case api.TypeIPv6:
		ip = make(net.IP, net.IPv6len)
		_, _ = random.Read(ip) // math/rand never returns an error

	default:
		return nil, false
	}

	return ip, true
}

// ipToBytes16 converts the given address into a size 16 byte array.
func ipToBytes16(address net.IP) [16]byte {
	var addressByte [16]byte
	copy(addressByte[:], address.To16())
	return addressByte
}

// isPublicIP checks if the given IP address is public or not.
//
// Note that this does not mean the address is routable in the real world. BGP feed needs to be analyzed for that.
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

// removeDuplicates returns a copy of in with duplicate elements removed, preserving the original order.
func removeDuplicates(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))

	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// toIPMap takes a list of addresses and converts them into a map where the keys are fixed size bytes.
func toIPMap(ips []net.IP) map[[16]byte]struct{} {
	m := make(map[[16]byte]struct{}, len(ips))
	for _, ip := range ips {
		var k [16]byte
		copy(k[:], ip.To16())
		m[k] = struct{}{}
	}
	return m
}
