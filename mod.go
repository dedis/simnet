package simnet

import (
	"fmt"
	"io"
	"os"

	"go.dedis.ch/simnet/sim"
)

// Simulation is a Kubernetes simulation.
type Simulation struct {
	strategy sim.Strategy
	round    sim.Round
	out      io.Writer
}

// NewSimulation creates a new simulation from the engine and the round.
func NewSimulation(r sim.Round, e sim.Strategy) *Simulation {
	return &Simulation{
		strategy: e,
		round:    r,
		out:      os.Stdout,
	}
}

// Run uses the round interface to run the simulation.
func (s *Simulation) Run() error {
	// TODO: flagset for options.

	defer func() {
		// Anything bad happening during the cleaning phase will be printed
		// so an error of the simulation is returned if any.

		err := s.strategy.Clean()
		if err != nil {
			fmt.Fprintf(s.out, "An error occured during cleaning: %v\n", err)
			fmt.Fprintln(s.out, "Please make sure to clean remaining components.")
		}
	}()

	err := s.strategy.Deploy()
	if err != nil {
		return err
	}

	err = s.strategy.Execute(s.round)
	if err != nil {
		return err
	}

	err = s.strategy.WriteStats("result.json")
	if err != nil {
		return err
	}

	return nil
}
