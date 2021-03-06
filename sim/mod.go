package sim

import (
	"context"
	"io"
	"time"
)

// ExecOptions is the options to pass to a command execution.
type ExecOptions struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// IO provides an API to interact with the nodes.
type IO interface {
	// Tag allows to mark a point in time with a given tag. The moment the
	// function is called will be saved and it can be reported to the plot.
	Tag(name string)

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

	// Disconnect provides an API to simulate a network failure between two
	// nodes.
	Disconnect(src string, targets ...string) error

	// Revert all the disconnection on the given node so that it will again be
	// able to contact all the nodes.
	Reconnect(node string) error

	// FetchStats gathers the statistics from the nodes and write into filename.
	FetchStats(from, to time.Time, filename string) error
}

// NodeInfo is the element value of the array available in the execution
// context.
// Use `nodes := ctx.Value(NodesKey{}).([]NodeInfo)` to retrieve the data.
type NodeInfo struct {
	Name    string
	Address string
}

// Round is executed during the simulation.
type Round interface {
	// Before is run once after deployment so that initialization can be
	// performed before the simulation is executed.
	Before(simio IO, nodes []NodeInfo) error

	// Execute is run during the execution step of the simulation, which is
	// after the nodes are deployed.
	Execute(simio IO, nodes []NodeInfo) error

	// After is run after each execution of the simulation to give a chance
	// to read files from simulation nodes.
	After(simio IO, nodes []NodeInfo) error
}

// Strategy provides the primitives to run a simulation from the
// deployment, to the execution of the simulation round and finally the
// cleaning.
type Strategy interface {
	Option(Option)

	// Deploy takes care of deploying the application according to the
	// topology. The simulation should be able to run after it returns
	// with no error.
	Deploy(context.Context, Round) error

	// Execute takes the round provided to execute a round of the simulation.
	Execute(context.Context, Round) error

	// WriteStats reads the data writtent by the monitors on each node of the
	// simulation.
	WriteStats(ctx context.Context, filename string) error

	// Clean wipes off any resources that has been created for the simulation.
	Clean(context.Context) error
}
