package strategies

import (
	"context"
)

// Round is executed during the simulation.
type Round interface {
	Execute(ctx context.Context)
}

// Simulation provides the primitives to run a simulation from the
// deployment, to the execution of the simulation round and finally the
// cleaning.
type Simulation interface {
	Deploy() error
	Execute(Round) error
	WriteStats(filepath string) error
	Clean() error
}
