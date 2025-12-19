// Copyright (c) 2025 Dioptra
// SPDX-License-Identifier: MIT
package generator

import (
	"context"
	"encoding/json"
	"errors"
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
	ErrCannotGeneratePD = errors.New("cannot generate pd")
	ErrDisconnected     = errors.New("disconnected")
)

// generator manages a persistent WebSocket connection to the orchestrator,
// receives system settings, and periodically emits probing directives to agents.
type generator struct {
	mu                           sync.RWMutex
	currentGlobalProbingRatePSPA uint
	currentSetOfAgentIDs         []string

	orchestratorURL string
	conn            *websocket.Conn
	rnd             *rand.Rand
}

func Run(parent context.Context, orchestratorAddr string, seed int64) error {
	m := &generator{
		orchestratorURL:              "ws://" + orchestratorAddr + "/generator",
		currentGlobalProbingRatePSPA: 1,
		currentSetOfAgentIDs:         []string{},
		rnd:                          rand.New(rand.NewSource(seed)),
	}

	// Connect to orchestrator WebSocket.
	conn, _, err := websocket.DefaultDialer.Dial(m.orchestratorURL, nil)
	if err != nil {
		return err
	}
	defer conn.Close()
	m.conn = conn

	log.Printf("generator connected to orchestrator at %s", m.orchestratorURL)

	g, ctx := errgroup.WithContext(parent)

	// Goroutine: receive settings (SystemStatus) and update local state.
	g.Go(func() error {
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()

			default:
				var status api.SystemStatus
				if err := m.conn.ReadJSON(&status); err != nil {
					return ErrDisconnected
				}

				m.mu.Lock()
				m.currentGlobalProbingRatePSPA = status.GlobalProbingRatePSPA
				m.currentSetOfAgentIDs = status.ActiveAgentIDs
				m.mu.Unlock()

				log.Printf("received update, probing_rate=%d num_agents=%d", m.currentGlobalProbingRatePSPA, len(m.currentSetOfAgentIDs))
			}
		}
	})

	// Goroutine: periodically generate and send ProbingDirectives.
	g.Go(func() error {
		for {
			pd, err := m.generateProbingDirectiveWithRateLimit(ctx)
			if err != nil {
				if err == ErrCannotGeneratePD {
					time.Sleep(500 * time.Millisecond)
					continue
				}
				return err
			}

			data, err := json.Marshal(pd)
			if err != nil {
				return err
			}
			if err := m.conn.WriteMessage(websocket.TextMessage, data); err != nil {
				return ErrDisconnected
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
	if err := g.Wait(); err != nil && err != ctx.Err() && err != ErrDisconnected {
		log.Printf("generator stopped with error: %v", err)
		return err
	}

	log.Println("generator stopped")
	return nil
}

func (m *generator) generateProbingDirectiveWithRateLimit(ctx context.Context) (*api.ProbingDirective, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.currentSetOfAgentIDs) == 0 || m.currentGlobalProbingRatePSPA == 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()

		default:
			return nil, ErrCannotGeneratePD
		}
	}

	waitingIntervalMs := time.Millisecond / time.Duration(2*int(m.currentGlobalProbingRatePSPA)*len(m.currentSetOfAgentIDs))

	select {
	case <-ctx.Done():
		return nil, ctx.Err()

	case <-time.After(waitingIntervalMs):
		pd := randomProbingDirective(m.rnd, m.currentSetOfAgentIDs)
		return pd, nil
	}
}

func randomProbingDirective(rnd *rand.Rand, agentIDs []string) *api.ProbingDirective {
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

	ttl := uint8(2 + rnd.Intn(30))

	var nh api.NextHeader
	switch proto {
	case api.ICMP:
		nh.ICMPNextHeader = &api.ICMPNextHeader{
			FirstHalfWord:  uint16(rnd.Intn(0x10000)),
			SecondHalfWord: uint16(rnd.Intn(0x10000)),
		}
	case api.UDP:
		nh.UDPNextHeader = &api.UDPNextHeader{
			SourcePort:      uint16(1024 + rnd.Intn(65535-1024)),
			DestinationPort: uint16(1024 + rnd.Intn(65535-1024)),
		}
	case api.ICMPv6:
		nh.ICMPv6NextHeader = &api.ICMPv6NextHeader{
			FirstHalfWord:  uint16(rnd.Intn(0x10000)),
			SecondHalfWord: uint16(rnd.Intn(0x10000)),
		}
	}

	return &api.ProbingDirective{
		IPVersion:          ipVersion,
		Protocol:           proto,
		AgentID:            agentID,
		DestinationAddress: dst,
		NearTTL:            ttl,
		NextHeader:         nh,
	}
}
