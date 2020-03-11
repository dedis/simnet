// +build !darwin

package docker

import (
	"io"

	"github.com/docker/docker/client"
)

// dockerOpenVPN is the implementation for a linux environment. It basically
// does nothing as the Docker network is accessible from the host directly.
type dockerOpenVPN struct{}

func newDockerOpenVPN(cli client.APIClient, out io.Writer, outDir string) dockerOpenVPN {
	return dockerOpenVPN{}
}

func (vpn dockerOpenVPN) Deploy() error {
	return nil
}

func (vpn dockerOpenVPN) Clean() error {
	return nil
}
