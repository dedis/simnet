package simnet

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.dedis.ch/simnet/engine"
)

type testRound struct{}

func (t testRound) Execute(ctx context.Context, tun engine.Tunnel) {}

type testEngine struct {
	errDeploy  error
	errExecute error
	errStats   error
	errClean   error
}

func (e *testEngine) Deploy() error {
	if e.errDeploy != nil {
		return e.errDeploy
	}

	return nil
}

func (e *testEngine) Execute(engine.Round) error {
	if e.errExecute != nil {
		return e.errExecute
	}

	return nil
}

func (e *testEngine) WriteStats(filepath string) error {
	if e.errStats != nil {
		return e.errStats
	}

	return nil
}

func (e *testEngine) Clean() error {
	if e.errClean != nil {
		return e.errClean
	}

	return nil
}

func TestSimulation_Run(t *testing.T) {
	engine := &testEngine{}
	sim := NewSimulation(testRound{}, engine)

	require.NoError(t, sim.Run())

	engine.errDeploy = errors.New("deploy")
	err := sim.Run()
	require.Error(t, err)
	require.Equal(t, "deploy", err.Error())

	engine.errDeploy = nil
	engine.errExecute = errors.New("execute")
	err = sim.Run()
	require.Error(t, err)
	require.Equal(t, "execute", err.Error())

	engine.errExecute = nil
	engine.errStats = errors.New("stats")
	err = sim.Run()
	require.Error(t, err)
	require.Equal(t, "stats", err.Error())

	engine.errStats = nil
	engine.errClean = errors.New("clean")
	err = sim.Run()
	require.Error(t, err)
	require.Equal(t, "clean", err.Error())
}
