package sim

import (
	"context"
)

// Round is executed during the simulation.
type Round interface {
	Execute(ctx context.Context) error
}

// Strategy provides the primitives to run a simulation from the
// deployment, to the execution of the simulation round and finally the
// cleaning.
type Strategy interface {
	Deploy() error
	Execute(Round) error
	WriteStats(filepath string) error
	Clean() error
}
