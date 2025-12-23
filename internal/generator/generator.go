// Copyright (c) 2025 Dioptra
// SPDX-License-Identifier: MIT
package generator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"sync"
	"time"

	"github.com/dioptra-io/retina-commons/pkg/api/v1"
	"github.com/gorilla/websocket"
	"golang.org/x/sync/errgroup"
)

var (
	ErrInvalidStatusForPDGeneration = errors.New("invalid status for PD generation")
)

// generator manages a persistent WebSocket connection to the orchestrator,
// receives system settings, and periodically emits probing directives to agents.
type generator struct {
	mu                  sync.Mutex
	currentSystemStatus *api.SystemStatus
	orchestratorURL     string
	notify              chan struct{}
	rnd                 *rand.Rand
}

func Run(parent context.Context, orchestratorAddr string, seed int64) error {
	g, ctx := errgroup.WithContext(parent)

	m := &generator{
		orchestratorURL: "ws://" + orchestratorAddr + "/generator",
		currentSystemStatus: &api.SystemStatus{
			GlobalProbingRatePSPA:          1,
			ProbingImpactLimitMS:           1000,
			DisallowedDestinationAddresses: []net.IP{},
			ActiveAgentIDs:                 []string{},
		},
		notify: make(chan struct{}, 1),
		rnd:    rand.New(rand.NewSource(seed)),
	}

	// Connect to orchestrator WebSocket.
	conn, _, err := websocket.DefaultDialer.Dial(m.orchestratorURL, nil)
	if err != nil {
		return err
	}
	defer conn.Close()

	log.Printf("generator connected to orchestrator at %s", m.orchestratorURL)

	// Goroutine: receive settings (SystemStatus) and update local state.
	g.Go(func() error {
		for {
			if err := m.updateCurrentStatus(ctx, conn); err != nil {
				return err
			}
		}
	})

	// Goroutine: periodically generate and send ProbingDirectives.
	g.Go(func() error {
		for {
			pd, err := m.generateProbingDirectiveWithRateLimit(ctx)
			if err != nil {
				return err
			}

			data, err := json.Marshal(pd)
			if err != nil {
				return err
			}

			m.mu.Lock()
			err = conn.WriteMessage(websocket.TextMessage, data)
			m.mu.Unlock()

			if err != nil {
				return err
			}
		}
	})

	// Goroutine: listen for the context and disconnect
	g.Go(func() error {
		<-ctx.Done()
		conn.Close()
		return ctx.Err()
	})

	// Wait until both loops end.
	if err := g.Wait(); err != nil && err != ctx.Err() && err != io.ErrUnexpectedEOF {
		log.Printf("generator stopped with error: %v", err)
		return err
	}

	log.Println("generator stopped")
	return nil
}

func (m *generator) updateCurrentStatus(ctx context.Context, conn *websocket.Conn) error {
	select {
	case <-ctx.Done():
		return ctx.Err()

	default:
		var status api.SystemStatus
		if err := conn.ReadJSON(&status); err != nil {
			return err
		}

		m.mu.Lock()
		m.currentSystemStatus = &status
		m.mu.Unlock()

		select {
		case m.notify <- struct{}{}:

		default:
		}

		log.Printf("received update: global_probing_rate_pspa=%d num_active_agent_ids=%d",
			status.GlobalProbingRatePSPA,
			len(status.ActiveAgentIDs))
		return nil
	}
}

func (m *generator) generateProbingDirectiveWithRateLimit(ctx context.Context) (*api.ProbingDirective, error) {
	for {
		m.mu.Lock()
		canGenerate := canGenerateProbes(m.currentSystemStatus)
		m.mu.Unlock()

		if canGenerate {
			break
		}

		// wait for either a state change or cancellation
		select {
		case <-ctx.Done():
			return nil, ctx.Err()

		case <-m.notify:
		}
	}

	m.mu.Lock()
	waitingIntervalMs := waitingTime(m.currentSystemStatus)
	activeAgentIDs := append([]string(nil), m.currentSystemStatus.ActiveAgentIDs...)
	m.mu.Unlock()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()

	case <-time.After(waitingIntervalMs):
		return randomProbingDirective(m.rnd, activeAgentIDs)
	}
}

func randomProbingDirective(rnd *rand.Rand, agentIDs []string) (*api.ProbingDirective, error) {
	// Validate agentIDs
	if len(agentIDs) == 0 {
		log.Println("lol")
		return nil, ErrInvalidStatusForPDGeneration
	}

	agentID := agentIDs[rnd.Intn(len(agentIDs))]

	ipVersion := api.TypeIPv4
	if rnd.Intn(2) == 1 {
		ipVersion = api.TypeIPv6
	}

	var proto api.Protocol
	if ipVersion == api.TypeIPv6 {
		if rnd.Intn(2) == 0 {
			proto = api.ICMPv6
		} else {
			proto = api.UDP
		}
	} else {
		if rnd.Intn(2) == 0 {
			proto = api.ICMP
		} else {
			proto = api.UDP
		}
	}

	var dst net.IP
	if ipVersion == api.TypeIPv4 {
		b := make([]byte, 4)
		rnd.Read(b)
		dst = net.IPv4(b[0], b[1], b[2], b[3]).To4()
	} else {
		b := make([]byte, 16)
		rnd.Read(b)
		dst = net.IP(b)
	}

	// Validate destination IP
	if dst == nil {
		log.Println("lol2")
		return nil, ErrInvalidStatusForPDGeneration
	}
	if ipVersion == api.TypeIPv4 && len(dst.To4()) != 4 {
		return nil, fmt.Errorf("%w: expected IPv4, got %v", ErrInvalidStatusForPDGeneration, dst)
	}
	if ipVersion == api.TypeIPv6 && len(dst) != 16 {
		return nil, fmt.Errorf("%w: expected IPv6, got %v", ErrInvalidStatusForPDGeneration, dst)
	}

	ttl := uint8(2 + rnd.Intn(30))

	// Validate TTL range (2-31 based on the logic above)
	if ttl < 2 || ttl > 31 {
		return nil, fmt.Errorf("%w: got %d, expected 2-31", ErrInvalidStatusForPDGeneration, ttl)
	}

	var nh api.NextHeader
	switch proto {
	case api.ICMP:
		if ipVersion != api.TypeIPv4 {
			return nil, fmt.Errorf("%w: ICMP requires IPv4", ErrInvalidStatusForPDGeneration)
		}
		nh.ICMPNextHeader = &api.ICMPNextHeader{
			FirstHalfWord:  uint16(rnd.Intn(0x10000)),
			SecondHalfWord: uint16(rnd.Intn(0x10000)),
		}
	case api.UDP:
		srcPort := uint16(1024 + rnd.Intn(65535-1024))
		dstPort := uint16(1024 + rnd.Intn(65535-1024))

		if srcPort < 1024 || dstPort < 1024 {
			return nil, fmt.Errorf("%w: srcPort=%d, dstPort=%d", ErrInvalidStatusForPDGeneration, srcPort, dstPort)
		}

		nh.UDPNextHeader = &api.UDPNextHeader{
			SourcePort:      srcPort,
			DestinationPort: dstPort,
		}
	case api.ICMPv6:
		if ipVersion != api.TypeIPv6 {
			return nil, fmt.Errorf("%w: ICMPv6 requires IPv6", ErrInvalidStatusForPDGeneration)
		}
		nh.ICMPv6NextHeader = &api.ICMPv6NextHeader{
			FirstHalfWord:  uint16(rnd.Intn(0x10000)),
			SecondHalfWord: uint16(rnd.Intn(0x10000)),
		}
	default:
		return nil, fmt.Errorf("%w: unknown protocol %v", ErrInvalidStatusForPDGeneration, proto)
	}

	// Validate next header was set
	if nh.ICMPNextHeader == nil && nh.UDPNextHeader == nil && nh.ICMPv6NextHeader == nil {
		log.Println("lol3")

		return nil, ErrInvalidStatusForPDGeneration
	}

	return &api.ProbingDirective{
		IPVersion:          ipVersion,
		Protocol:           proto,
		AgentID:            agentID,
		DestinationAddress: dst,
		NearTTL:            ttl,
		NextHeader:         nh,
	}, nil
}
