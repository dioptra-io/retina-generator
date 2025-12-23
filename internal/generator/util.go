package generator

import (
	"time"

	"github.com/dioptra-io/retina-commons/pkg/api/v1"
)

func canGenerateProbes(status *api.SystemStatus) bool {
	return len(status.ActiveAgentIDs) != 0 && status.GlobalProbingRatePSPA != 0
}

func waitingTime(status *api.SystemStatus) time.Duration {
	return time.Millisecond * 1000
}
