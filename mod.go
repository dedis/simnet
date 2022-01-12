package simnet

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"go.dedis.ch/simnet/sim"
	"golang.org/x/xerrors"
)

var doCleaning bool
var doDeploy bool
var doExecute bool
var doStats bool
var vpnCommand string

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

	flagset := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flagset.BoolVar(&doCleaning, "do-clean", false, "override the usual flow to only wipe simulation resources")
	flagset.BoolVar(&doDeploy, "do-deploy", false, "override the usual flow to only deploy simulation resources")
	flagset.BoolVar(&doExecute, "do-execute", false, "override the usual flow to only run the simulation round")
	flagset.BoolVar(&doStats, "do-stats", false, "override the usual flow to only write statistics")
	flagset.StringVar(&vpnCommand, "vpn", defaultOpenVPNPath, "path to the OpenVPN executable")

	flagset.Parse(args[1:])

	doAll := !doCleaning && !doDeploy && !doExecute && !doStats

	fmt.Fprintf(s.out, "Using strategy %v\n", s.strategy)

	// Set global options to the strategy.
	s.strategy.Option(sim.WithVPN(vpnCommand))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if doCleaning || doAll {
		defer func() {
			// Anything bad happening during the cleaning phase will be printed
			// so an error of the simulation is returned if any.

			fmt.Println("Starting cleaning...")

			err := s.strategy.Clean(ctx)
			if err != nil {
				fmt.Fprintln(s.out, "An error occurred during cleaning: ", err)
				fmt.Fprintln(s.out, "Please make sure to clean remaining components.")
			}
		}()
	}

	if doDeploy || doAll {
		err := s.strategy.Deploy(ctx, s.round)
		if err != nil {
			return err
		}
	}

	if doExecute || doAll {
		err := s.strategy.Execute(ctx, s.round)
		if err != nil {
			return err
		}

		err = s.strategy.WriteStats(ctx, "result.json")
		if err != nil {
			return err
		}
	}

	if doStats {
		fmt.Fprintln(s.out, "Write statistics only")

		// The user can request to only write the statistics to the file.
		err := s.strategy.WriteStats(ctx, "result.json")
		if err != nil {
			return xerrors.Errorf("couldn't write statistics: %v", err)
		}
	}

	return nil
}
