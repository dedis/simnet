package docker

import (
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"go.dedis.ch/simnet/network"
	"go.dedis.ch/simnet/sim"
)

type testRound struct{}

func (r testRound) Execute(ctx context.Context) error {

	return nil
}

func TestStrategy_Main(t *testing.T) {
	s, err := newStrategy(
		sim.WithImage(
			"dedis/conode:latest",
			[]string{"bash", "-c"},
			[]string{"/root/conode setup --non-interactive --port 7770 && /root/conode -d 2 server"},
			sim.NewTCP(7770),
			sim.NewTCP(7771),
		),
		sim.WithTopology(network.NewSimpleTopology(3, 0)),
		sim.WithFileMapper(
			sim.FilesKey("private.toml"),
			sim.FileMapper{
				Path: "/root/.config/conode/private.toml",
				Mapper: func(r io.Reader) (interface{}, error) {
					return nil, nil
				},
			},
		),
	)
	require.NoError(t, err)

	err = s.Deploy()
	require.NoError(t, err)

	err = s.Execute(testRound{})
	require.NoError(t, err)

	err = s.Clean()
	require.NoError(t, err)
}
