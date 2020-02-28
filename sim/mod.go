package sim

import (
	"io"
)

type ExecOptions struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// IO provides an API to interact with the nodes.
type IO interface {
	// Read reads a file on a simulation node at the given path. It returns a
	// stream through a reader, or an error if something bad happened.
	Read(node, path string) (io.ReadCloser, error)

	// Write writes a file on a simulation node at the given path. It will
	// write everything from the reader until it reaches EOF. It also returns
	// an error if something bad happened.
	Write(node, path string, content io.Reader) error

	// Exec executes a command on a simulation node and returns the output if
	// the command is successful, an error otherwise.
	Exec(node string, cmd []string, options ExecOptions) error
}

// NodeInfoKey is the single value key that is available in execution context
// to get information about the simulation nodes.
type NodeInfoKey struct{}

// NodeInfo is the element value of the array available in the execution
// context.
// Use `nodes := ctx.Value(NodesKey{}).([]NodeInfo)` to retrieve the data.
type NodeInfo struct {
	Name    string
	Address string
}

// Round is executed during the simulation.
type Round interface {
	// Configure is run before Execute so that initialization can be performed
	// before the simulation is executed.
	Configure(simio IO, nodes []NodeInfo) error

	// Execute is run during the execution step of the simulation, which is
	// after the nodes are deployed.
	Execute(simio IO, nodes []NodeInfo) error
}

// Strategy provides the primitives to run a simulation from the
// deployment, to the execution of the simulation round and finally the
// cleaning.
type Strategy interface {
	// Deploy takes care of deploying the application according to the
	// topology. The simulation should be able to run after it returns
	// with no error.
	Deploy(Round) error

	// Execute takes the round provided to execute a round of the simulation.
	Execute(Round) error

	// WriteStats reads the data writtent by the monitors on each node of the
	// simulation.
	WriteStats(filename string) error

	// Clean wipes off any resources that has been created for the simulation.
	Clean() error
}
