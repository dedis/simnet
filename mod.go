package simnet

import "go.dedis.ch/simnet/sim"

// Simulation is a Kubernetes simulation.
type Simulation struct {
	strategy sim.Strategy
	round    sim.Round
}

// NewSimulation creates a new simulation from the engine and the round.
func NewSimulation(r sim.Round, e sim.Strategy) *Simulation {
	return &Simulation{strategy: e, round: r}
}

// Run uses the round interface to run the simulation.
func (s *Simulation) Run() (err error) {
	// TODO: flagset for options.

	err = s.strategy.Deploy()
	if err != nil {
		return
	}

	defer func() {
		errClean := s.strategy.Clean()
		if errClean != nil {
			err = errClean
		}
	}()

	err = s.strategy.Execute(s.round)
	if err != nil {
		return
	}

	err = s.strategy.WriteStats("result.json")
	if err != nil {
		return
	}

	// Error is populated by the cleaning at the end if any error happens during
	// the procedure.
	return
}
