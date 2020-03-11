package sim

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.dedis.ch/simnet/network"
)

func TestOption_Output(t *testing.T) {
	// With a value
	options := NewOptions([]Option{WithOutput("abc")})
	require.Equal(t, "abc", options.OutputDir)

	// Default with working home directory
	homeDir, err := os.UserHomeDir()
	require.NoError(t, err)

	options = NewOptions([]Option{WithOutput("")})
	require.Equal(t, filepath.Join(homeDir, ".config", "simnet"), options.OutputDir)

	// Default with home directory not accessible.
	userHomeDir = func() (string, error) {
		return "", errors.New("oops")
	}
	defer func() {
		userHomeDir = os.UserHomeDir
	}()

	options = NewOptions([]Option{WithOutput("")})
	require.Equal(t, filepath.Join(".config", "simnet"), options.OutputDir)
}

func TestOption_Topology(t *testing.T) {
	topo := network.NewSimpleTopology(5, 0)
	options := NewOptions([]Option{WithTopology(topo)})

	require.Equal(t, topo.Len(), options.Topology.Len())

	require.Equal(t, "tcp", TCP.String())
	require.Equal(t, "udp", UDP.String())
}

func TestOption_Image(t *testing.T) {
	options := NewOptions([]Option{WithImage(
		"path/to/image",
		[]string{"cmd"},
		[]string{"arg1", "arg2"},
		NewTCP(2000),
		NewUDP(3000),
		NewTCP(3001),
	)})

	require.Equal(t, "path/to/image", options.Image)
	require.Equal(t, "cmd", options.Cmd[0])
	require.Equal(t, []string{"arg1", "arg2"}, options.Args)
	require.Equal(t, TCP, options.Ports[0].Protocol())
	require.Equal(t, int32(2000), options.Ports[0].Value())
	require.Equal(t, UDP, options.Ports[1].Protocol())
	require.Equal(t, int32(3000), options.Ports[1].Value())
	require.Equal(t, TCP, options.Ports[2].Protocol())
	require.Equal(t, int32(3001), options.Ports[2].Value())

	options = NewOptions([]Option{WithImage(
		"nginx",
		nil,
		nil,
	)})

	require.Equal(t, "library/nginx", options.Image)
}

func TestOption_VPN(t *testing.T) {
	options := NewOptions([]Option{WithVPN("abc")})

	require.Equal(t, options.VPNExecutable, "abc")
}
