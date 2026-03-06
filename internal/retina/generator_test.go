// ## Test Coverage
//
// Tests for the retina-generator internal package, covering PD generation,
// HTTP communication with the orchestrator, and input validation.
//
// Coverage:
// - NewGen: 100% - All validation rules
// - Run: partial - Failure path via IP exhaustion is unreachable in practice
// - sendPDs: partial - Marshal error is unreachable since ProbingDirective is always serializable
// - generatePD: partial - generateAddress error and IP exhaustion are unreachable in practice
// - generateAddress: 100% - IPv4, IPv6, and invalid version
// - isPublic: 100% - All address categories
// - main(): 0% (untested) - Standard practice for main functions with os.Exit
//
// ## Testing Strategy
//
// Uses httptest.Server to simulate orchestrator HTTP responses without
// requiring actual network connections. All tests are deterministic via
// fixed random seeds.

package retina

import (
	"context"
	"encoding/json"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dioptra-io/retina-commons/api/v1"
)

// ============================================================================
// TEST HELPERS
// ============================================================================

func defaultConfig() *Config {
	return &Config{
		Seed:            42,
		MinTTL:          4,
		MaxTTL:          32,
		AgentIDs:        []string{"agent-1"},
		NumPDs:          10,
		OrchestratorURL: "http://localhost:8080",
		HTTPTimeout:     10 * time.Second,
	}
}

func newTestServer(t *testing.T, statusCode int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected application/json, got %s", ct)
		}
		var received []*api.ProbingDirective
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Errorf("invalid JSON body: %v", err)
		}
		w.WriteHeader(statusCode)
	}))
}

// ============================================================================
// UNIT TESTS - NewGen
// ============================================================================

func TestNewGen_Valid(t *testing.T) {
	t.Parallel()

	_, err := NewGen(defaultConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewGen_EmptyAgentIDs(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()
	cfg.AgentIDs = []string{}

	_, err := NewGen(cfg)
	if err == nil {
		t.Fatal("expected error for empty agent IDs")
	}
}

func TestNewGen_MinTTLGreaterThanMaxTTL(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()
	cfg.MinTTL = 32
	cfg.MaxTTL = 4

	_, err := NewGen(cfg)
	if err == nil {
		t.Fatal("expected error when min TTL > max TTL")
	}
}

func TestNewGen_ZeroNumPDs(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()
	cfg.NumPDs = 0

	_, err := NewGen(cfg)
	if err == nil {
		t.Fatal("expected error for zero NumPDs")
	}
}

func TestNewGen_EmptyOrchestratorURL(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()
	cfg.OrchestratorURL = ""

	_, err := NewGen(cfg)
	if err == nil {
		t.Fatal("expected error for empty orchestrator address")
	}
}

func TestNewGen_NegativeHTTPTimeout(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()
	cfg.HTTPTimeout = -1

	_, err := NewGen(cfg)
	if err == nil {
		t.Fatal("expected error for negative HTTP timeout")
	}
}

// ============================================================================
// UNIT TESTS - Run
// ============================================================================

func TestRun_Success(t *testing.T) {
	t.Parallel()

	server := newTestServer(t, http.StatusOK)
	defer server.Close()

	cfg := defaultConfig()
	cfg.OrchestratorURL = server.URL
	gen, _ := NewGen(cfg)

	if err := gen.Run(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRun_OrchestratorError(t *testing.T) {
	t.Parallel()

	server := newTestServer(t, http.StatusInternalServerError)
	defer server.Close()

	cfg := defaultConfig()
	cfg.OrchestratorURL = server.URL
	gen, _ := NewGen(cfg)

	if err := gen.Run(context.Background()); err == nil {
		t.Fatal("expected error but got nil")
	}
}

// ============================================================================
// UNIT TESTS - sendPDs
// ============================================================================

func TestSendPDs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		statusCode     int
		expectError    bool
		expectedErrMsg string
	}{
		{
			name:        "200 OK",
			statusCode:  http.StatusOK,
			expectError: false,
		},
		{
			name:           "400 Bad Request",
			statusCode:     http.StatusBadRequest,
			expectError:    true,
			expectedErrMsg: "400",
		},
		{
			name:           "500 Internal Server Error",
			statusCode:     http.StatusInternalServerError,
			expectError:    true,
			expectedErrMsg: "500",
		},
		{
			name:           "Unexpected Status",
			statusCode:     http.StatusTeapot,
			expectError:    true,
			expectedErrMsg: "unexpected",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := newTestServer(t, tt.statusCode)
			defer server.Close()

			pds := []*api.ProbingDirective{{}}
			err := sendPDs(context.Background(), pds, server.URL, 2*time.Second)

			if tt.expectError && err == nil {
				t.Fatalf("expected error but got nil")
			}
			if !tt.expectError && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.expectError && tt.expectedErrMsg != "" && err != nil {
				if !strings.Contains(err.Error(), tt.expectedErrMsg) {
					t.Fatalf("expected error containing %q, got %v", tt.expectedErrMsg, err)
				}
			}
		})
	}
}

func TestSendPDs_Timeout(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
	}))
	defer server.Close()

	pds := []*api.ProbingDirective{{}}
	err := sendPDs(context.Background(), pds, server.URL, 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestSendPDs_InvalidURL(t *testing.T) {
	t.Parallel()

	pds := []*api.ProbingDirective{{}}
	err := sendPDs(context.Background(), pds, "://invalid-url", 2*time.Second)
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

// ============================================================================
// UNIT TESTS - generatePD
// ============================================================================

func TestGeneratePD_Success(t *testing.T) {
	t.Parallel()

	r := rand.New(rand.NewSource(42))
	agents := []string{"a1", "a2", "a3"}

	pd, err := generatePD(r, 100, agents, 5, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pd == nil {
		t.Fatal("expected non-nil ProbingDirective")
	}
	if pd.ProbingDirectiveID != 100 {
		t.Fatalf("unexpected ID: %d", pd.ProbingDirectiveID)
	}
	if pd.NearTTL < 5 || pd.NearTTL > 10 {
		t.Fatalf("TTL out of bounds: %d", pd.NearTTL)
	}
	if pd.DestinationAddress == nil {
		t.Fatal("expected non-nil destination address")
	}
	if !isPublic(pd.DestinationAddress) {
		t.Fatalf("generated IP is not public: %v", pd.DestinationAddress)
	}

	switch pd.Protocol {
	case api.UDP:
		if pd.NextHeader.UDPNextHeader == nil {
			t.Fatal("expected UDP header")
		}
	case api.ICMP:
		if pd.NextHeader.ICMPNextHeader == nil {
			t.Fatal("expected ICMP header")
		}
	case api.ICMPv6:
		if pd.NextHeader.ICMPv6NextHeader == nil {
			t.Fatal("expected ICMPv6 header")
		}
	}
}

// TestGeneratePD_Distinct verifies that multiple PDs generated from the same
// seed produce distinct destination addresses, catching regressions where
// rand is re-seeded inside the loop.
func TestGeneratePD_Distinct(t *testing.T) {
	t.Parallel()

	r := rand.New(rand.NewSource(42))
	agents := []string{"a1"}

	pd1, err := generatePD(r, 0, agents, 5, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pd2, err := generatePD(r, 1, agents, 5, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if pd1.DestinationAddress.Equal(pd2.DestinationAddress) {
		t.Error("expected distinct destination addresses but got identical ones")
	}
}

// ============================================================================
// UNIT TESTS - generateAddress
// ============================================================================

func TestGenerateAddress_IPv4(t *testing.T) {
	t.Parallel()

	r := rand.New(rand.NewSource(42))

	ip, err := generateAddress(r, api.IPv4)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ip) != net.IPv4len {
		t.Fatalf("expected IPv4 length %d, got %d", net.IPv4len, len(ip))
	}
}

func TestGenerateAddress_IPv6(t *testing.T) {
	t.Parallel()

	r := rand.New(rand.NewSource(42))

	ip, err := generateAddress(r, api.IPv6)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ip) != net.IPv6len {
		t.Fatalf("expected IPv6 length %d, got %d", net.IPv6len, len(ip))
	}
}

func TestGenerateAddress_InvalidVersion(t *testing.T) {
	t.Parallel()

	r := rand.New(rand.NewSource(42))

	_, err := generateAddress(r, 99)
	if err == nil {
		t.Fatal("expected error for invalid IP version")
	}
}

// ============================================================================
// UNIT TESTS - isPublic
// ============================================================================

func TestIsPublic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		ip     net.IP
		public bool
	}{
		{net.ParseIP("8.8.8.8"), true},
		{net.ParseIP("1.1.1.1"), true},
		{net.ParseIP("192.168.1.1"), false},
		{net.ParseIP("10.0.0.1"), false},
		{net.ParseIP("127.0.0.1"), false},
		{net.ParseIP("0.0.0.0"), false},
		{net.ParseIP("224.0.0.1"), false},
		{nil, false},
	}

	for _, tt := range tests {
		if got := isPublic(tt.ip); got != tt.public {
			t.Errorf("isPublic(%v) = %v, expected %v", tt.ip, got, tt.public)
		}
	}
}
