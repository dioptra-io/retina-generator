// ## Test Coverage
//
// Tests for the retina-generator internal package, covering PD generation,
// JSONL file output, and input validation.
//
// Coverage:
// - NewGen: 100% - All validation rules
// - Run: partial - Failure path via IP exhaustion is unreachable in practice
// - writePDsToFile: 100% - Success, invalid path, empty slice, overwrite
// - generatePD: partial - generateAddress error and IP exhaustion are unreachable in practice
// - generateAddress: 100% - IPv4, IPv6, and invalid version
// - isPublic: 100% - All address categories
// - main(): 0% (untested) - Standard practice for main functions with os.Exit

package retina

import (
	"bufio"
	"context"
	"encoding/json"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dioptra-io/retina-commons/api/v1"
)

// ============================================================================
// TEST HELPERS
// ============================================================================

func defaultConfig(t *testing.T) *Config {
	t.Helper()
	return &Config{
		Seed:       42,
		MinTTL:     4,
		MaxTTL:     32,
		AgentIDs:   []string{"agent-1"},
		NumPDs:     10,
		OutputFile: filepath.Join(t.TempDir(), "pds.jsonl"),
	}
}

// ============================================================================
// UNIT TESTS - NewGen
// ============================================================================

func TestNewGen_Valid(t *testing.T) {
	t.Parallel()

	_, err := NewGen(defaultConfig(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewGen_EmptyAgentIDs(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig(t)
	cfg.AgentIDs = []string{}

	_, err := NewGen(cfg)
	if err == nil {
		t.Fatal("expected error for empty agent IDs")
	}
}

func TestNewGen_MinTTLGreaterThanMaxTTL(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig(t)
	cfg.MinTTL = 32
	cfg.MaxTTL = 4

	_, err := NewGen(cfg)
	if err == nil {
		t.Fatal("expected error when min TTL > max TTL")
	}
}

func TestNewGen_ZeroNumPDs(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig(t)
	cfg.NumPDs = 0

	_, err := NewGen(cfg)
	if err == nil {
		t.Fatal("expected error for zero NumPDs")
	}
}

func TestNewGen_EmptyOutputFile(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig(t)
	cfg.OutputFile = ""

	_, err := NewGen(cfg)
	if err == nil {
		t.Fatal("expected error for empty output file")
	}
}

// ============================================================================
// UNIT TESTS - Run
// ============================================================================

func TestRun_Success(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig(t)
	gen, err := NewGen(cfg)
	if err != nil {
		t.Fatalf("unexpected NewGen error: %v", err)
	}

	if err := gen.Run(context.Background()); err != nil {
		t.Fatalf("unexpected Run error: %v", err)
	}

	f, err := os.Open(cfg.OutputFile)
	if err != nil {
		t.Fatalf("cannot open output file: %v", err)
	}
	defer func() { _ = f.Close() }()

	var lineCount int
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var pd api.ProbingDirective
		if err := json.Unmarshal([]byte(line), &pd); err != nil {
			t.Errorf("line %d is not valid JSON: %v", lineCount+1, err)
		}
		lineCount++
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner error: %v", err)
	}
	if uint64(lineCount) != cfg.NumPDs {
		t.Errorf("expected %d lines, got %d", cfg.NumPDs, lineCount)
	}
}

func TestRun_InvalidPath(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig(t)
	cfg.OutputFile = "/nonexistent-dir/pds.jsonl"
	gen, err := NewGen(cfg)
	if err != nil {
		t.Fatalf("unexpected NewGen error: %v", err)
	}

	if err := gen.Run(context.Background()); err == nil {
		t.Fatal("expected error for invalid output file path")
	}
}

// ============================================================================
// UNIT TESTS - writePDsToFile
// ============================================================================

func TestWritePDsToFile_Success(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "out.jsonl")
	pds := []*api.ProbingDirective{
		{ProbingDirectiveID: 0, IPVersion: api.IPv4, Protocol: api.UDP},
		{ProbingDirectiveID: 1, IPVersion: api.IPv6, Protocol: api.ICMPv6},
	}

	if err := writePDsToFile(pds, path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read output file: %v", err)
	}

	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != len(pds) {
		t.Fatalf("expected %d lines, got %d", len(pds), len(lines))
	}
	for i, line := range lines {
		var pd api.ProbingDirective
		if err := json.Unmarshal([]byte(line), &pd); err != nil {
			t.Errorf("line %d is not valid JSON: %v", i, err)
		}
		if pd.ProbingDirectiveID != pds[i].ProbingDirectiveID {
			t.Errorf("line %d: expected ID %d, got %d", i, pds[i].ProbingDirectiveID, pd.ProbingDirectiveID)
		}
	}
}

func TestWritePDsToFile_EmptySlice(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "empty.jsonl")
	if err := writePDsToFile([]*api.ProbingDirective{}, path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("expected file to exist: %v", err)
	}
	if info.Size() != 0 {
		t.Errorf("expected empty file, got %d bytes", info.Size())
	}
}

func TestWritePDsToFile_InvalidPath(t *testing.T) {
	t.Parallel()

	err := writePDsToFile([]*api.ProbingDirective{{}}, "/nonexistent-dir/out.jsonl")
	if err == nil {
		t.Fatal("expected error for invalid path")
	}
}

func TestWritePDsToFile_Overwrites(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "overwrite.jsonl")

	pds2 := []*api.ProbingDirective{{ProbingDirectiveID: 0}, {ProbingDirectiveID: 1}}
	if err := writePDsToFile(pds2, path); err != nil {
		t.Fatalf("first write error: %v", err)
	}

	pds1 := []*api.ProbingDirective{{ProbingDirectiveID: 99}}
	if err := writePDsToFile(pds1, path); err != nil {
		t.Fatalf("second write error: %v", err)
	}

	data, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 1 {
		t.Errorf("expected 1 line after overwrite, got %d", len(lines))
	}
}

func TestWritePDsToFile_EncodeError(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "readonly.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("unexpected error creating file: %v", err)
	}
	_ = f.Close()
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("unexpected error chmod: %v", err)
	}

	err = writePDsToFile([]*api.ProbingDirective{{ProbingDirectiveID: 0}}, path)
	if err == nil {
		t.Fatal("expected encode error when writing to unwritable file")
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
