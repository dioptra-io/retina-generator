package retina

import (
	"context"
	"encoding/json"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dioptra-io/retina-commons/api/v1"
)

func TestSend(t *testing.T) {
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

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

				// Verify method
				if r.Method != http.MethodPost {
					t.Errorf("expected POST, got %s", r.Method)
				}

				// Verify content type
				if ct := r.Header.Get("Content-Type"); ct != "application/json" {
					t.Errorf("expected application/json, got %s", ct)
				}

				// Verify body is valid JSON
				var received []*api.ProbingDirective
				if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
					t.Errorf("invalid JSON body: %v", err)
				}

				w.WriteHeader(tt.statusCode)
			}))
			defer server.Close()

			directives := []*api.ProbingDirective{
				{}, // minimal valid object
			}

			err := sendPDs(context.Background(), directives, server.URL, 2*time.Second)

			if tt.expectError && err == nil {
				t.Fatalf("expected error but got nil")
			}

			if !tt.expectError && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.expectError && tt.expectedErrMsg != "" && err != nil {
				if !contains(err.Error(), tt.expectedErrMsg) {
					t.Fatalf("expected error containing %q, got %v", tt.expectedErrMsg, err)
				}
			}
		})
	}
}

func TestGenerateAddress(t *testing.T) {
	r := rand.New(rand.NewSource(42))

	ip4, err := generateAddress(r, api.IPv4)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ip4) != net.IPv4len {
		t.Fatalf("expected IPv4 length %d, got %d", net.IPv4len, len(ip4))
	}

	ip6, err := generateAddress(r, api.IPv6)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ip6) != net.IPv6len {
		t.Fatalf("expected IPv6 length %d, got %d", net.IPv6len, len(ip6))
	}
}

func TestGenerateAddress_InvalidVersion(t *testing.T) {
	r := rand.New(rand.NewSource(42))

	_, err := generateAddress(r, 99)
	if err == nil {
		t.Fatal("expected error for invalid IP version")
	}
}

func TestIsPublicIP(t *testing.T) {
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
		if got := isPublicIP(tt.ip); got != tt.public {
			t.Fatalf("isPublicIP(%v) = %v, expected %v", tt.ip, got, tt.public)
		}
	}
}

func TestGeneratePD_NoAgents(t *testing.T) {
	r := rand.New(rand.NewSource(42))

	_, err := generatePD(r, 1, 10, nil, 1, 64)
	if err == nil {
		t.Fatal("expected error when no agentIDs provided")
	}
}

func TestGeneratePD_Success(t *testing.T) {
	r := rand.New(rand.NewSource(42))

	agents := []string{"a1", "a2", "a3"}

	pd, err := generatePD(r, 100, 100, agents, 5, 10)
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

	if !isPublicIP(pd.DestinationAddress) {
		t.Fatalf("generated IP is not public: %v", pd.DestinationAddress)
	}

	// Protocol/header consistency check
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

func TestGeneratePD_MaxTriesExceeded(t *testing.T) {
	r := rand.New(rand.NewSource(1))

	agents := []string{"a1"}

	_, err := generatePD(r, 1, 0, agents, 1, 1)
	if err == nil {
		t.Fatal("expected error when maxNumTries is 0")
	}
}

// simple helper to avoid importing strings just for Contains
func contains(s, substr string) bool {
	return len(substr) == 0 || (len(s) >= len(substr) &&
		(len(s) == len(substr) && s == substr ||
			len(s) > len(substr) &&
				(func() bool {
					for i := 0; i <= len(s)-len(substr); i++ {
						if s[i:i+len(substr)] == substr {
							return true
						}
					}
					return false
				})()))
}
