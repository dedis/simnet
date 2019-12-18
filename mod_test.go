package simnet

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.dedis.ch/simnet/sim"
)

type testRound struct{}

func (t testRound) Execute(ctx context.Context) error {
	return nil
}

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

func (e *testStrategy) Execute(sim.Round) error {
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
	buffer := new(bytes.Buffer)
	sim.out = buffer

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
	require.NoError(t, err)
	require.Contains(t, buffer.String(), "An error occured during cleaning")
}
