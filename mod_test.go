package simnet

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"go.dedis.ch/simnet/sim"
)

type testRound struct{}

func (t testRound) Before(simio sim.IO, nodes []sim.NodeInfo) error {
	return nil
}

func (t testRound) Execute(simio sim.IO, nodes []sim.NodeInfo) error {
	return nil
}

func (t testRound) After(simio sim.IO, nodes []sim.NodeInfo) error {
	return nil
}

type testStrategy struct {
	errDeploy  error
	errExecute error
	errStats   error
	errClean   error
}

func (e *testStrategy) Option(sim.Option) {}

func (e *testStrategy) Deploy(context.Context, sim.Round) error {
	if e.errDeploy != nil {
		return e.errDeploy
	}

	return nil
}

func (e *testStrategy) Execute(context.Context, sim.Round) error {
	if e.errExecute != nil {
		return e.errExecute
	}

	return nil
}

func (e *testStrategy) WriteStats(ctx context.Context, filepath string) error {
	if e.errStats != nil {
		return e.errStats
	}

	return nil
}

func (e *testStrategy) Clean(context.Context) error {
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

	args := []string{os.Args[0]}
	require.NoError(t, sim.Run(args))

	err := sim.Run([]string{})
	require.Error(t, err)
	require.True(t, errors.Is(err, errMissingArgs))

	stry.errDeploy = errors.New("deploy")
	err = sim.Run(args)
	require.EqualError(t, err, "couldn't deploy experiment: deploy")

	stry.errDeploy = nil
	stry.errExecute = errors.New("execute")
	err = sim.Run(args)
	require.EqualError(t, err, "couldn't execute experiment: execute")

	stry.errExecute = nil
	stry.errStats = errors.New("stats")
	err = sim.Run(args)
	require.EqualError(t, err, "couldn't write statistics: stats")

	stry.errStats = nil
	stry.errClean = errors.New("clean")
	err = sim.Run(args)
	require.NoError(t, err)
	require.Contains(t, buffer.String(), "An error occurred during cleaning")

	args = []string{os.Args[0], "--do-stats"}
	stry.errStats = errors.New("oops")
	err = sim.Run(args)
	require.EqualError(t, err, "couldn't write statistics: oops")
}
