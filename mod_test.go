package simnet

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.dedis.ch/simnet/strategies"
)

type testRound struct{}

func (t testRound) Execute(ctx context.Context, tun strategies.Tunnel) {}

type testStrategy struct {
	errDeploy  error
	errExecute error
	errStats   error
	errClean   error
}

func (e *testStrategy) Deploy() error {
	if e.errDeploy != nil {
		return e.errDeploy
	}

	return nil
}

func (e *testStrategy) Execute(strategies.Round) error {
	if e.errExecute != nil {
		return e.errExecute
	}

	return nil
}

func (e *testStrategy) WriteStats(filepath string) error {
	if e.errStats != nil {
		return e.errStats
	}

	return nil
}

func (e *testStrategy) Clean() error {
	if e.errClean != nil {
		return e.errClean
	}

	return nil
}

func TestSimulation_Run(t *testing.T) {
	stry := &testStrategy{}
	sim := NewSimulation(testRound{}, stry)

	require.NoError(t, sim.Run())

	stry.errDeploy = errors.New("deploy")
	err := sim.Run()
	require.Error(t, err)
	require.Equal(t, "deploy", err.Error())

	stry.errDeploy = nil
	stry.errExecute = errors.New("execute")
	err = sim.Run()
	require.Error(t, err)
	require.Equal(t, "execute", err.Error())

	stry.errExecute = nil
	stry.errStats = errors.New("stats")
	err = sim.Run()
	require.Error(t, err)
	require.Equal(t, "stats", err.Error())

	stry.errStats = nil
	stry.errClean = errors.New("clean")
	err = sim.Run()
	require.Error(t, err)
	require.Equal(t, "clean", err.Error())
}
