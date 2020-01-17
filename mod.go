package simnet

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"go.dedis.ch/simnet/sim"
)

var doCleaning bool
var doDeploy bool
var doExecute bool

var errMissingArgs = errors.New("expect at least one argument")

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
func (s *Simulation) Run(args []string) error {
	if len(args) == 0 {
		return errMissingArgs
	}

	flagset := flag.NewFlagSet(args[0], flag.ExitOnError)
	flagset.BoolVar(&doCleaning, "do-clean", false, "override the usual flow to only wipe simulation resources")
	flagset.BoolVar(&doDeploy, "do-deploy", false, "override the usual flow to only deploy simulation resources")
	flagset.BoolVar(&doExecute, "do-execute", false, "override the usual flow to only run the simulation round")

	flagset.Parse(args[1:])

	doAll := !doCleaning && !doDeploy && !doExecute

	fmt.Fprintf(s.out, "Using strategy %v\n", s.strategy)

	if doCleaning || doAll {
		defer func() {
			// Anything bad happening during the cleaning phase will be printed
			// so an error of the simulation is returned if any.

			err := s.strategy.Clean()
			if err != nil {
				fmt.Fprintf(s.out, "An error occured during cleaning: %v\n", err)
				fmt.Fprintln(s.out, "Please make sure to clean remaining components.")
			}
		}()
	}

	if doDeploy || doAll {
		err := s.strategy.Deploy(s.round)
		if err != nil {
			return err
		}
	}

	if doExecute || doAll {
		err := s.strategy.Execute(s.round)
		if err != nil {
			return err
		}

		err = s.strategy.WriteStats("result.json")
		if err != nil {
			return err
		}
	}

	return nil
}
