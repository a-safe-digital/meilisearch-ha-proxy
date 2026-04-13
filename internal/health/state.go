package health

import "fmt"

// State represents the health state of a node.
type State int

const (
	// Healthy means the node is responding to health checks.
	Healthy State = iota
	// Suspect means the node has failed some health checks but hasn't
	// crossed the unhealthy threshold yet.
	Suspect
	// Unhealthy means the node has failed enough consecutive health
	// checks to be considered down.
	Unhealthy
)

func (s State) String() string {
	switch s {
	case Healthy:
		return "healthy"
	case Suspect:
		return "suspect"
	case Unhealthy:
		return "unhealthy"
	default:
		return fmt.Sprintf("unknown(%d)", int(s))
	}
}
