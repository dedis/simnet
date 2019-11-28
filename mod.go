package simnet

import (
	"go.dedis.ch/simnet/engine"
)

// Simulation is a Kubernetes simulation.
type Simulation struct {
	engine engine.SimulationEngine
	round  engine.Round
}

// NewSimulation creates a new simulation that can be deployed on Kubernetes.
func NewSimulation(cfg string, r engine.Round, opts ...engine.Option) (*Simulation, error) {
	engine, err := engine.NewKubernetesEngine(cfg, opts...)
	if err != nil {
		return nil, err
	}

	return &Simulation{engine: engine, round: r}, nil
}

// Run uses the round interface to run the simulation.
func (sim *Simulation) Run() error {
	err := sim.engine.Deploy()
	if err != nil {
		return err
	}

	defer sim.engine.Clean()

	err = sim.engine.Execute(sim.round)
	if err != nil {
		return err
	}

	err = sim.engine.WriteStats("result.json")
	if err != nil {
		return err
	}

	return nil
}
