package docker

import (
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/go-connections/nat"
	"github.com/stretchr/testify/require"
	"go.dedis.ch/simnet/network"
)

func TestStrategy_Main(t *testing.T) {
	s, err := newStrategy(
		WithContainer(container.Config{
			Image: "dedis/conode:latest",
			Cmd:   []string{"bash", "-c", "/root/conode setup --non-interactive --port 7770 && /root/conode -d 2 server"},
			ExposedPorts: nat.PortSet{
				"7770": struct{}{},
				"7771": struct{}{},
			},
		}),
		WithTopology(network.NewSimpleTopology(3, 0)),
	)
	require.NoError(t, err)

	err = s.Deploy()
	require.NoError(t, err)

	err = s.Clean()
	require.NoError(t, err)
}
