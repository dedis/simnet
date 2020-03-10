package docker

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/strslice"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"go.dedis.ch/simnet/sim"
)

const (
	routerContainerName   = "simnet-router"
	initContainerName     = "simnet-router-init"
	volumeName            = "simnet-router-volume"
	simnetRouterImage     = "dedis/simnet-router"
	simnetRouterInitImage = "dedis/simnet-router-init"
)

// VPN is the interface to deploy and connect to a VPN inside the Docker
// private network area.
type VPN interface {
	Deploy() error
	Clean() error
}

type dockerOpenVPN struct {
	outDir string
	cli    client.APIClient
	tun    sim.Tunnel
}

func newDockerOpenVPN(cli client.APIClient, outDir string) dockerOpenVPN {
	return dockerOpenVPN{
		outDir: filepath.Join(outDir, "vpn"),
		cli:    cli,
		tun:    sim.NewDefaultTunnel(outDir),
	}
}

func (vpn dockerOpenVPN) Deploy() error {
	ctx := context.Background()

	err := vpn.createVolume(ctx)
	if err != nil {
		return err
	}

	err = vpn.createRouterContainer(ctx)
	if err != nil {
		return err
	}

	err = vpn.connect()
	if err != nil {
		return err
	}

	return nil
}

func (vpn dockerOpenVPN) createVolume(ctx context.Context) error {
	vol, err := vpn.cli.VolumeCreate(ctx, volume.VolumesCreateBody{Name: volumeName})
	if err != nil {
		return err
	}

	fmt.Printf("Volume created: %s\n", vol.Name)

	hcfg := &container.HostConfig{
		AutoRemove: true,
		Mounts: []mount.Mount{
			{
				Type:   mount.TypeBind,
				Source: vpn.outDir,
				Target: "/etc/openvpn",
			},
		},
	}

	cfg := &container.Config{
		Image: simnetRouterInitImage,
	}

	resp, err := vpn.cli.ContainerCreate(ctx, cfg, hcfg, nil, initContainerName)
	if err != nil {
		return err
	}

	err = vpn.cli.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{})
	if err != nil {
		return err
	}

	_, err = vpn.cli.ContainerWait(ctx, initContainerName)
	if err != nil {
		return err
	}

	return nil
}

func (vpn dockerOpenVPN) createRouterContainer(ctx context.Context) error {
	hcfg := &container.HostConfig{
		AutoRemove: true,
		Privileged: true,
		CapAdd:     strslice.StrSlice{"NET_ADMIN"},
		PortBindings: nat.PortMap{
			nat.Port("1194/udp"): []nat.PortBinding{
				{
					HostIP:   "127.0.01",
					HostPort: "1194",
				},
			},
		},
		Mounts: []mount.Mount{
			{
				// Type:   mount.TypeVolume,
				// Source: volumeName,
				Type:   mount.TypeBind,
				Source: vpn.outDir,
				Target: "/etc/openvpn",
			},
		},
	}

	cfg := &container.Config{Image: simnetRouterImage}

	resp, err := vpn.cli.ContainerCreate(ctx, cfg, hcfg, nil, routerContainerName)
	if err != nil {
		return err
	}

	err = vpn.cli.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{})
	if err != nil {
		return err
	}

	return nil
}

func (vpn dockerOpenVPN) connect() error {
	certs := sim.Certificates{
		CA:   filepath.Join(vpn.outDir, "pki/ca.crt"),
		Cert: filepath.Join(vpn.outDir, "pki/issued/client1.crt"),
		Key:  filepath.Join(vpn.outDir, "pki/private/client1.key"),
	}

	err := vpn.tun.Start(sim.WithHost("127.0.0.1"), sim.WithPort(1194), sim.WithCertificate(certs))
	if err != nil {
		return err
	}

	return nil
}

func (vpn dockerOpenVPN) Clean() error {
	ctx := context.Background()

	timeout := 10 * time.Second

	if vpn.tun != nil {
		err := vpn.tun.Stop()
		if err != nil {
			return err
		}
	}

	err := vpn.cli.ContainerStop(ctx, routerContainerName, &timeout)
	if err != nil {
		return err
	}

	// TODO: better
	time.Sleep(2 * time.Second)

	err = vpn.cli.VolumeRemove(ctx, volumeName, true)
	if err != nil {
		return err
	}

	return nil
}
