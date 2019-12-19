package kubernetes

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.dedis.ch/simnet/network"
	apiv1 "k8s.io/api/core/v1"
)

func TestOption_Output(t *testing.T) {
	// With a value
	options := NewOptions([]Option{WithOutput("abc")})
	require.Equal(t, "abc", options.output)

	// Default with working home directory
	homeDir, err := os.UserHomeDir()
	require.NoError(t, err)

	options = NewOptions([]Option{WithOutput("")})
	require.Equal(t, filepath.Join(homeDir, ".config", "simnet"), options.output)

	// Default with home directory not accessible.
	userHomeDir = func() (string, error) {
		return "", errors.New("oops")
	}
	defer func() {
		userHomeDir = os.UserHomeDir
	}()

	options = NewOptions([]Option{WithOutput("")})
	require.Equal(t, filepath.Join(".config", "simnet"), options.output)
}

func TestOption_FileMapper(t *testing.T) {
	e := errors.New("oops")
	options := NewOptions([]Option{WithFileMapper(FilesKey("abc"), FileMapper{
		Path: "/path/to/file",
		Mapper: func(r io.Reader) (interface{}, error) {
			return nil, e
		},
	})})

	fm, ok := options.files[FilesKey("abc")]
	require.True(t, ok)
	require.Equal(t, "/path/to/file", fm.Path)
	_, err := fm.Mapper(nil)
	require.Equal(t, e, err)

	fm, ok = options.files[FilesKey("")]
	require.False(t, ok)
}

func TestOption_Topology(t *testing.T) {
	topo := network.NewSimpleTopology(5, 0)
	options := NewOptions([]Option{WithTopology(topo)})

	require.Equal(t, topo.Len(), options.topology.Len())
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

	require.Equal(t, ContainerAppName, options.container.Name)
	require.Equal(t, "path/to/image", options.container.Image)
	require.Equal(t, "cmd", options.container.Command[0])
	require.Equal(t, []string{"arg1", "arg2"}, options.container.Args)
	require.Equal(t, apiv1.ProtocolTCP, options.container.Ports[0].Protocol)
	require.Equal(t, int32(2000), options.container.Ports[0].ContainerPort)
	require.Equal(t, apiv1.ProtocolUDP, options.container.Ports[1].Protocol)
	require.Equal(t, int32(3000), options.container.Ports[1].ContainerPort)
	require.Equal(t, apiv1.ProtocolTCP, options.container.Ports[2].Protocol)
	require.Equal(t, int32(3001), options.container.Ports[2].ContainerPort)
}
