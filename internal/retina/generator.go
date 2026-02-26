package retina

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/dioptra-io/retina-commons/api/v1"
)

// Config contains the configuration parameters for the PD generation.
type Config struct {
	// Seed is the seed used by the random generator.
	Seed int64
	// MinTTL is the minimum possible TTL value for the generated PD.
	MinTTL uint8
	// MaxTTL is the maximum possible TTL value for the generated PD.
	MaxTTL uint8
	// AgentIDs is the list of agent IDs that the generated ProbingDirectives
	// are assigned to.
	AgentIDs []string
	// NumPDs is the number of ProbingDirectives to generate.
	NumPDs uint64
	// OrchestratorAddress is the address of the orchestrator.
	OrchestratorAddress string
	// HTTPTimeout is the timeout value for the request. Zero means no timeout.
	HTTPTimeout time.Duration
}

// gen is the implementation of the retina-generator. It generates the given
// number of probing directives and posts them to the orchestrator. When this
// operation is completed it exits with a nil error.
type gen struct {
	config *Config
}

// NewGenFromConfig generates a new generator from the given config. If the
// config has non-valid values it returns an error.
func NewGenFromConfig(config *Config) (*gen, error) {
	if len(config.AgentIDs) == 0 {
		return nil, fmt.Errorf("agentIDs cannot be empty")
	}
	if config.MinTTL > config.MaxTTL {
		return nil, fmt.Errorf("minTTL cannot be greather than maxTTL")
	}
	config.OrchestratorAddress = strings.TrimRight(config.OrchestratorAddress, "/")

	return &gen{
		config: config,
	}, nil
}

// Run, will generate the requested number of ProbingDirectives and exit with a
// nil error.
func (g *gen) Run(ctx context.Context) error {
	// Generate the directives.
	directives := make([]*api.ProbingDirective, 0, g.config.NumPDs)
	for i := uint64(0); i < g.config.NumPDs; i++ {
		directive, err := generatePD(
			rand.New(rand.NewSource(g.config.Seed)),
			i,
			g.config.NumPDs,
			g.config.AgentIDs,
			g.config.MinTTL,
			g.config.MaxTTL)
		if err != nil {
			log.Printf("cannot generate %d ProbingDirectives: %v", g.config.NumPDs, err)
		}

		directives = append(directives, directive)
	}

	// Prepare the url.
	url := fmt.Sprintf("http://%v/%v", g.config.OrchestratorAddress, "directives")

	// Send the directives to the orchestrator.
	return sendPDs(ctx, directives, url, g.config.HTTPTimeout)
}

// sendPDs makes a http POST request to the given url.
func sendPDs(ctx context.Context, directives []*api.ProbingDirective, url string, timeout time.Duration) error {
	if len(directives) == 0 {
		return fmt.Errorf("cannot choose agentID: given list of agentIDs is empty")
	}

	// Marshal JSON
	body, err := json.Marshal(directives)
	if err != nil {
		return fmt.Errorf("marshal directives: %w", err)
	}

	// Context with timeout
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{
		Timeout: timeout,
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusBadRequest:
		return fmt.Errorf("orchestrator returned 400 (bad request)")
	case http.StatusInternalServerError:
		return fmt.Errorf("orchestrator returned 500 (internal server error)")
	default:
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}
}

// generatePD generates the given number of Probing Directives from the provided
// arguments.
func generatePD(random *rand.Rand, id, maxNumTries uint64, agentIDs []string, minTTL, maxTTL uint8) (*api.ProbingDirective, error) {
	if len(agentIDs) == 0 {
		return nil, fmt.Errorf("cannot select an agentID: there are no agents")
	}
	agentID := agentIDs[random.Intn(len(agentIDs))]

	// Randomize IPVersion
	ipVersion := []api.IPVersion{
		api.IPv4,
		api.IPv6,
	}[random.Intn(2)]

	// Randomize Protocol
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

	// Randomize DestinationAddress
	var destinationAddress net.IP
	for range maxNumTries {
		candidateAddress, err := generateAddress(random, ipVersion)
		if err != nil {
			return nil, fmt.Errorf("cannot generate address: %w", err)
		}
		if !isPublicIP(candidateAddress) {
			continue
		}

		destinationAddress = candidateAddress
		break
	}
	if destinationAddress == nil {
		return nil, fmt.Errorf("cannot generate IP address: exceeded max number of tries")
	}

	// Randomize NearTTL
	nearTTL := minTTL + uint8(random.Intn(int(maxTTL-minTTL+1)))

	// Randomize NextHeader
	nextHeader := api.NextHeader{}
	switch protocol {
	case api.ICMP:
		nextHeader.ICMPNextHeader = &api.ICMPNextHeader{
			FirstHalfWord:  uint16(random.Intn(1 << 16)),
			SecondHalfWord: uint16(random.Intn(1 << 16)),
		}
	case api.UDP:
		nextHeader.UDPNextHeader = &api.UDPNextHeader{
			SourcePort:      uint16(random.Intn(1 << 16)),
			DestinationPort: uint16(random.Intn(1 << 16)),
		}
	case api.ICMPv6:
		nextHeader.ICMPv6NextHeader = &api.ICMPv6NextHeader{
			FirstHalfWord:  uint16(random.Intn(1 << 16)),
			SecondHalfWord: uint16(random.Intn(1 << 16)),
		}
	}

	return &api.ProbingDirective{
		ProbingDirectiveID: id,
		IPVersion:          ipVersion,
		Protocol:           protocol,
		AgentID:            agentID,
		DestinationAddress: destinationAddress,
		NearTTL:            nearTTL,
		NextHeader:         nextHeader,
	}, nil
}

// generateAddress generates a random public IP address for the given IP version.
func generateAddress(random *rand.Rand, ipVersion api.IPVersion) (net.IP, error) {
	var ip net.IP

	switch ipVersion {
	case api.IPv4:
		ip = make(net.IP, net.IPv4len)
		_, _ = random.Read(ip) // math/rand never returns an error

	case api.IPv6:
		ip = make(net.IP, net.IPv6len)
		_, _ = random.Read(ip) // math/rand never returns an error

	default:
		return nil, fmt.Errorf("invalid ip version: expected 4 or 6, got %v", ipVersion)
	}

	return ip, nil
}

// isPublicIP checks if the given IP address is public or not.
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
