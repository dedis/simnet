package strategies

import (
	"context"
)

// Tunnel is an interface that will create tunnels to node of the simulation so
// that simulation running on a private network can be accessible on a per
// needed basis.
type Tunnel interface {
	Create(base int, ipaddr string, exec func(addr string)) error
}

// Round is executed during the simulation.
type Round interface {
	Execute(ctx context.Context, tun Tunnel)
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
