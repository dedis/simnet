package simnet

import (
	"go.dedis.ch/simnet/engine"
)

// Simulation is a Kubernetes simulation.
type Simulation struct {
	engine engine.SimulationEngine
	round  engine.Round
}

// NewSimulation creates a new simulation from the engine and the round.
func NewSimulation(r engine.Round, e engine.SimulationEngine) *Simulation {
	return &Simulation{engine: e, round: r}
}

// Run uses the round interface to run the simulation.
func (sim *Simulation) Run() (err error) {
	err = sim.engine.Deploy()
	if err != nil {
		return
	}

	defer func() {
		errClean := sim.engine.Clean()
		if errClean != nil {
			err = errClean
		}
	}()

	err = sim.engine.Execute(sim.round)
	if err != nil {
		return
	}

	err = sim.engine.WriteStats("result.json")
	if err != nil {
		return
	}

	// Error is populated by the cleaning at the end if any error happens during
	// the procedure.
	return
}
