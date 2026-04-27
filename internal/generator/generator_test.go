// Copyright (c) 2026 Sorbonne Université
// SPDX-License-Identifier: MIT
//
// Tests for the retina-generator internal package, covering PD generation,
// JSONL file output, and input validation.
//
// Partial coverage notes:
//   - Run, generatePD: IP exhaustion path is unreachable in practice
//   - writePDsToFile: encode error is unreachable on a successfully opened file
//   - buildNextHeader: default panic branch is unreachable; protocol is always ICMP, UDP, or ICMPv6
//   - main(): untested by convention

package generator

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dioptra-io/retina-commons/api/v1"
)

// -- test helpers -------------------------------------------------------------

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

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

// -- NewGen -------------------------------------------------------------------

func TestNewGen_Valid(t *testing.T) {
	t.Parallel()

	_, err := NewGen(defaultConfig(t), discardLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewGen_EmptyAgentIDs(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig(t)
	cfg.AgentIDs = []string{}

	_, err := NewGen(cfg, discardLogger())
	if err == nil {
		t.Fatal("expected error for empty agent IDs")
	}
}

func TestNewGen_MinTTLGreaterThanMaxTTL(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig(t)
	cfg.MinTTL = 32
	cfg.MaxTTL = 4

	_, err := NewGen(cfg, discardLogger())
	if err == nil {
		t.Fatal("expected error when min TTL > max TTL")
	}
}

func TestNewGen_ZeroNumPDs(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig(t)
	cfg.NumPDs = 0

	_, err := NewGen(cfg, discardLogger())
	if err == nil {
		t.Fatal("expected error for zero NumPDs")
	}
}

func TestNewGen_EmptyOutputFile(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig(t)
	cfg.OutputFile = ""

	_, err := NewGen(cfg, discardLogger())
	if err == nil {
		t.Fatal("expected error for empty output file")
	}
}

func TestNewGen_WithBlocklist(t *testing.T) {
	t.Parallel()

	blocklistPath := filepath.Join(t.TempDir(), "blocklist.txt")
	if err := os.WriteFile(blocklistPath, []byte("10.0.0.0/8\n"), 0600); err != nil { //nolint:gosec // G306: test file
		t.Fatalf("cannot write blocklist file: %v", err)
	}

	cfg := defaultConfig(t)
	cfg.BlocklistFile = blocklistPath

	gen, err := NewGen(cfg, discardLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gen == nil {
		t.Fatal("expected non-nil generator")
	}
}

func TestNewGen_InvalidBlocklistFile(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig(t)
	cfg.BlocklistFile = "/nonexistent/blocklist.txt"

	_, err := NewGen(cfg, discardLogger())
	if err == nil {
		t.Fatal("expected error for invalid blocklist file")
	}
}

// -- Run ----------------------------------------------------------------------

func TestRun_Success(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig(t)
	gen, err := NewGen(cfg, discardLogger())
	if err != nil {
		t.Fatalf("unexpected NewGen error: %v", err)
	}

	if err := gen.Run(context.Background()); err != nil {
		t.Fatalf("unexpected Run error: %v", err)
	}

	f, err := os.Open(cfg.OutputFile) //nolint:gosec // G304: path from t.TempDir()
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
	gen, err := NewGen(cfg, discardLogger())
	if err != nil {
		t.Fatalf("unexpected NewGen error: %v", err)
	}

	if err := gen.Run(context.Background()); err == nil {
		t.Fatal("expected error for invalid output file path")
	}
}

func TestRun_WithBlocklist(t *testing.T) {
	t.Parallel()

	blocklistPath := filepath.Join(t.TempDir(), "blocklist.txt")
	if err := os.WriteFile(blocklistPath, []byte("10.0.0.0/8\n"), 0600); err != nil { //nolint:gosec // G306: test file
		t.Fatalf("cannot write blocklist file: %v", err)
	}

	cfg := defaultConfig(t)
	cfg.BlocklistFile = blocklistPath
	cfg.NumPDs = 100

	gen, err := NewGen(cfg, discardLogger())
	if err != nil {
		t.Fatalf("unexpected NewGen error: %v", err)
	}

	if err := gen.Run(context.Background()); err != nil {
		t.Fatalf("unexpected Run error: %v", err)
	}

	data, err := os.ReadFile(cfg.OutputFile) //nolint:gosec // G304: path from t.TempDir()
	if err != nil {
		t.Fatalf("cannot read output file: %v", err)
	}

	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) == 0 {
		t.Fatal("expected some PDs to be generated")
	}
}

func TestRun_ContextCancelled(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig(t)
	cfg.NumPDs = 1000000
	gen, err := NewGen(cfg, discardLogger())
	if err != nil {
		t.Fatalf("unexpected NewGen error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := gen.Run(ctx); err == nil {
		t.Fatal("expected error for canceled context")
	}
}

// -- writePDsToFile -----------------------------------------------------------

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

	data, err := os.ReadFile(path) //nolint:gosec // G304: path from t.TempDir()
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

	data, _ := os.ReadFile(path) //nolint:gosec // G304: path from t.TempDir()
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 1 {
		t.Errorf("expected 1 line after overwrite, got %d", len(lines))
	}
}

// -- generatePD ---------------------------------------------------------------

func TestGeneratePD_Success(t *testing.T) {
	t.Parallel()

	r := rand.New(rand.NewSource(42)) //nolint:gosec // G404: test helper, deterministic seed
	agents := []string{"a1", "a2", "a3"}

	pd, err := generatePD(r, 100, agents, 5, 10, nil)
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

	r := rand.New(rand.NewSource(42)) //nolint:gosec // G404: test helper, deterministic seed
	agents := []string{"a1"}

	pd1, err := generatePD(r, 0, agents, 5, 10, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pd2, err := generatePD(r, 1, agents, 5, 10, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if pd1.DestinationAddress.Equal(pd2.DestinationAddress) {
		t.Error("expected distinct destination addresses but got identical ones")
	}
}

// -- buildNextHeader ---------------------------------------------------------

func TestBuildNextHeader_AllProtocols(t *testing.T) {
	t.Parallel()

	r := rand.New(rand.NewSource(42)) //nolint:gosec // G404: test helper, deterministic seed
	for _, protocol := range []api.Protocol{api.ICMP, api.UDP, api.ICMPv6} {
		nh := buildNextHeader(r, protocol)
		switch protocol {
		case api.ICMP:
			if nh.ICMPNextHeader == nil {
				t.Errorf("expected ICMPNextHeader for ICMP")
			}
		case api.UDP:
			if nh.UDPNextHeader == nil {
				t.Errorf("expected UDPNextHeader for UDP")
			}
		case api.ICMPv6:
			if nh.ICMPv6NextHeader == nil {
				t.Errorf("expected ICMPv6NextHeader for ICMPv6")
			}
		}
	}
}

// -- generateAddress ----------------------------------------------------------

func TestGenerateAddress_IPv4(t *testing.T) {
	t.Parallel()

	r := rand.New(rand.NewSource(42)) //nolint:gosec // G404: test helper, deterministic seed

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

	r := rand.New(rand.NewSource(42)) //nolint:gosec // G404: test helper, deterministic seed

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

	r := rand.New(rand.NewSource(42)) //nolint:gosec // G404: test helper, deterministic seed

	_, err := generateAddress(r, 99)
	if err == nil {
		t.Fatal("expected error for invalid IP version")
	}
}

// -- isPublic -----------------------------------------------------------------

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

// -- parseBlocklist -----------------------------------------------------------

func TestParseBlocklist_Valid(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "blocklist.txt")
	content := "10.0.0.0/8\n192.168.0.0/16\n# comment\n2001:db8::/32\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil { //nolint:gosec // G306: test file
		t.Fatalf("cannot write test file: %v", err)
	}

	blocklist, err := parseBlocklist(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(blocklist) != 3 {
		t.Fatalf("expected 3 networks, got %d", len(blocklist))
	}
}

func TestParseBlocklist_InvalidPath(t *testing.T) {
	t.Parallel()

	_, err := parseBlocklist("/nonexistent/path/blocklist.txt")
	if err == nil {
		t.Fatal("expected error for invalid path")
	}
}

func TestParseBlocklist_InvalidCIDR(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "blocklist.txt")
	if err := os.WriteFile(path, []byte("invalid-cidr\n"), 0600); err != nil { //nolint:gosec // G306: test file
		t.Fatalf("cannot write test file: %v", err)
	}

	_, err := parseBlocklist(path)
	if err == nil {
		t.Fatal("expected error for invalid CIDR")
	}
}

func TestParseBlocklist_EmptyAndComments(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "blocklist.txt")
	content := "# comment\n\n10.0.0.0/8\n\n# another comment\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil { //nolint:gosec // G306: test file
		t.Fatalf("cannot write test file: %v", err)
	}

	blocklist, err := parseBlocklist(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(blocklist) != 1 {
		t.Fatalf("expected 1 network, got %d", len(blocklist))
	}
}

func TestParseBlocklist_ScannerError(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "blocklist.txt")
	content := strings.Repeat("a", 1024*1024)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil { //nolint:gosec // G306: test file
		t.Fatalf("cannot write test file: %v", err)
	}

	_, err := parseBlocklist(path)
	if err == nil {
		t.Fatal("expected error for scanner error (line too long)")
	}
}

// -- isBlocked ----------------------------------------------------------------

func TestIsBlocked(t *testing.T) {
	t.Parallel()

	_, blocklist1, _ := net.ParseCIDR("10.0.0.0/8")
	_, blocklist2, _ := net.ParseCIDR("192.168.0.0/16")
	blocklist := []*net.IPNet{blocklist1, blocklist2}

	tests := []struct {
		ip      net.IP
		blocked bool
	}{
		{net.ParseIP("10.1.2.3"), true},
		{net.ParseIP("192.168.1.1"), true},
		{net.ParseIP("8.8.8.8"), false},
		{net.ParseIP("172.16.0.1"), false},
	}

	for _, tt := range tests {
		if got := isBlocked(tt.ip, blocklist); got != tt.blocked {
			t.Errorf("isBlocked(%v) = %v, expected %v", tt.ip, got, tt.blocked)
		}
	}
}

func TestIsBlocked_EmptyBlocklist(t *testing.T) {
	t.Parallel()

	if isBlocked(net.ParseIP("10.0.0.1"), nil) {
		t.Error("expected false for empty blocklist")
	}
	if isBlocked(net.ParseIP("10.0.0.1"), []*net.IPNet{}) {
		t.Error("expected false for nil blocklist")
	}
}
