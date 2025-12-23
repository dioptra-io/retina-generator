// Copyright (c) 2025 Dioptra
// SPDX-License-Identifier: MIT
package generator

import (
	"context"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dioptra-io/retina-commons/pkg/api/v1"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRandomProbingDirective_IPv4ICMP(t *testing.T) {
	rnd := rand.New(rand.NewSource(42))
	agentIDs := []string{"agent-1", "agent-2", "agent-3"}

	// Generate multiple directives to cover different random paths
	for i := 0; i < 100; i++ {
		pd, err := randomProbingDirective(rnd, agentIDs)
		assert.NoError(t, err)

		assert.NotNil(t, pd)
		assert.Contains(t, agentIDs, pd.AgentID)
		assert.NotNil(t, pd.DestinationAddress)

		if pd.IPVersion == api.TypeIPv4 {
			assert.Len(t, pd.DestinationAddress.To4(), 4)
		} else {
			assert.Equal(t, api.TypeIPv6, pd.IPVersion)
			assert.Len(t, pd.DestinationAddress, 16)
		}

		assert.GreaterOrEqual(t, pd.NearTTL, uint8(2))
		assert.LessOrEqual(t, pd.NearTTL, uint8(31))

		// Verify protocol matches IP version
		switch pd.IPVersion {
		case api.TypeIPv4:
			assert.True(t, pd.Protocol == api.ICMP || pd.Protocol == api.UDP)
			if pd.Protocol == api.ICMP {
				assert.NotNil(t, pd.NextHeader.ICMPNextHeader)
			} else {
				assert.NotNil(t, pd.NextHeader.UDPNextHeader)
			}
		case api.TypeIPv6:
			assert.True(t, pd.Protocol == api.ICMPv6 || pd.Protocol == api.UDP)
			if pd.Protocol == api.ICMPv6 {
				assert.NotNil(t, pd.NextHeader.ICMPv6NextHeader)
			} else {
				assert.NotNil(t, pd.NextHeader.UDPNextHeader)
			}
		}
	}
}

func TestRandomProbingDirective_UDPPortRange(t *testing.T) {
	rnd := rand.New(rand.NewSource(12345))
	agentIDs := []string{"agent-1"}

	for i := 0; i < 1000; i++ {
		pd, err := randomProbingDirective(rnd, agentIDs)
		assert.NoError(t, err)

		if pd.NextHeader.UDPNextHeader != nil {
			assert.GreaterOrEqual(t, pd.NextHeader.UDPNextHeader.SourcePort, uint16(1024))
			assert.GreaterOrEqual(t, pd.NextHeader.UDPNextHeader.DestinationPort, uint16(1024))
		}
	}
}

func TestRandomProbingDirective_DeterministicWithSeed(t *testing.T) {
	seed := int64(999)
	agentIDs := []string{"agent-a", "agent-b"}

	rnd1 := rand.New(rand.NewSource(seed))
	rnd2 := rand.New(rand.NewSource(seed))

	for i := 0; i < 10; i++ {
		pd1, err1 := randomProbingDirective(rnd1, agentIDs)
		pd2, err2 := randomProbingDirective(rnd2, agentIDs)

		assert.NoError(t, err1)
		assert.NoError(t, err2)
		assert.Equal(t, pd1.AgentID, pd2.AgentID)
		assert.Equal(t, pd1.IPVersion, pd2.IPVersion)
		assert.Equal(t, pd1.Protocol, pd2.Protocol)
		assert.Equal(t, pd1.DestinationAddress.String(), pd2.DestinationAddress.String())
		assert.Equal(t, pd1.NearTTL, pd2.NearTTL)
	}
}

func TestGenerator_UpdateCurrentStatus_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	g := &generator{
		currentSystemStatus: &api.SystemStatus{},
		notify:              make(chan struct{}, 1),
	}

	// Create a mock connection that would block on ReadJSON
	var upgrader = websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()
		// Don't send anything, just wait
		time.Sleep(10 * time.Second)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer conn.Close()

	err = g.updateCurrentStatus(ctx, conn)
	assert.Equal(t, context.Canceled, err)
}

func TestGenerator_NotifyChannel(t *testing.T) {
	g := &generator{
		notify: make(chan struct{}, 1),
	}

	// First notification should succeed
	select {
	case g.notify <- struct{}{}:
		// OK
	default:
		t.Fatal("notify channel should accept first message")
	}

	// Second notification should not block (channel is buffered with size 1)
	select {
	case g.notify <- struct{}{}:
		// This means buffer was consumed somehow
	default:
		// Expected - channel is full
	}
}

func TestGenerator_GenerateProbingDirectiveWithRateLimit_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	g := &generator{
		currentSystemStatus: &api.SystemStatus{
			GlobalProbingRatePSPA: 0, // No probes allowed
			ActiveAgentIDs:        []string{},
		},
		notify: make(chan struct{}, 1),
		rnd:    rand.New(rand.NewSource(42)),
	}

	// Cancel after a short delay
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	pd, err := g.generateProbingDirectiveWithRateLimit(ctx)
	assert.Nil(t, pd)
	assert.Equal(t, context.Canceled, err)
}

func TestRun_ConnectionError(t *testing.T) {
	ctx := context.Background()

	// Try to connect to non-existent server
	err := Run(ctx, "localhost:99999", 42)
	assert.Error(t, err)
}

// Benchmark for randomProbingDirective
func BenchmarkRandomProbingDirective(b *testing.B) {
	rnd := rand.New(rand.NewSource(42))
	agentIDs := []string{"agent-1", "agent-2", "agent-3", "agent-4", "agent-5"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := randomProbingDirective(rnd, agentIDs)
		assert.NoError(b, err)
	}
}
