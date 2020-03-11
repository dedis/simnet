package docker

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVPN(t *testing.T) {
	vpn := newDockerOpenVPN(nil, nil, nil)
	require.NoError(t, vpn.Deploy())
	require.NoError(t, vpn.Clean())
}
