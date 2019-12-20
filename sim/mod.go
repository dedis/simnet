package sim

import (
	"context"
)

// Round is executed during the simulation.
type Round interface {
	// Execute is run during the execution step of the simulation, which is
	// after the nodes are deployed.
	Execute(ctx context.Context) error
}

// Strategy provides the primitives to run a simulation from the
// deployment, to the execution of the simulation round and finally the
// cleaning.
type Strategy interface {
	// Deploy takes care of deploying the application according to the
	// topology. The simulation should be able to run after it returns
	// with no error.
	Deploy() error

	// Execute takes the round provided to execute a round of the simulation.
	Execute(Round) error

	// WriteStats reads the data writtent by the monitors on each node of the
	// simulation.
	WriteStats(filename string) error

	// Clean wipes off any resources that has been created for the simulation.
	Clean() error
}
