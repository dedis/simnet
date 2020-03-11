package docker

import (
	"context"
	"io"
	"path/filepath"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/strslice"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"go.dedis.ch/simnet/sim"
)

const (
	routerContainerName   = "simnet-router"
	initContainerName     = "simnet-router-init"
	simnetRouterImage     = "dedis/simnet-router"
	simnetRouterInitImage = "dedis/simnet-router-init"
)

// dockerOpenVPN is the VPN implementation for Darwin based systems so that it
// creates a tunnel between the host and the guest Docker VM.
type dockerOpenVPN struct {
	out     io.Writer
	outDir  string
	cli     client.APIClient
	tun     sim.Tunnel
	options *sim.Options
}

func newDockerOpenVPN(cli client.APIClient, out io.Writer, options *sim.Options) dockerOpenVPN {
	vpnOutDir := filepath.Join(options.OutputDir, "vpn")

	return dockerOpenVPN{
		out:     out,
		outDir:  vpnOutDir,
		cli:     cli,
		tun:     sim.NewDefaultTunnel(vpnOutDir),
		options: options,
	}
}

func (vpn dockerOpenVPN) Deploy() error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// Get the image for the router container.
	err := pullImage(ctx, vpn.cli, simnetRouterImage, vpn.out)
	if err != nil {
		return err
	}

	// Get the image for the initialization container.
	err = pullImage(ctx, vpn.cli, simnetRouterInitImage, vpn.out)
	if err != nil {
		return err
	}

	// Run the init container to generate the certificates.
	err = vpn.generateCerts(ctx)
	if err != nil {
		return err
	}

	// Deploy the router.
	err = vpn.createRouterContainer(ctx)
	if err != nil {
		return err
	}

	// Open a connection to the VPN.
	err = vpn.connect()
	if err != nil {
		return err
	}

	return nil
}

func (vpn dockerOpenVPN) generateCerts(ctx context.Context) error {
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
					HostIP:   "127.0.0.1",
					HostPort: "1194",
				},
			},
		},
		Mounts: []mount.Mount{
			{
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
		CA:   filepath.Join(vpn.outDir, "pki", "ca.crt"),
		Cert: filepath.Join(vpn.outDir, "pki", "issued", "client1.crt"),
		Key:  filepath.Join(vpn.outDir, "pki", "private", "client1.key"),
	}

	err := vpn.tun.Start(
		sim.WithHost("127.0.0.1"),
		sim.WithPort(1194),
		sim.WithCertificate(certs),
		sim.WithCommand(vpn.options.VPNExecutable),
	)
	if err != nil {
		return err
	}

	return nil
}

func (vpn dockerOpenVPN) Clean() error {
	ctx := context.Background()

	timeout := 10 * time.Second
	errs := make([]error, 0)

	if vpn.tun != nil {
		err := vpn.tun.Stop()
		if err != nil {
			errs = append(errs, err)
		}
	}

	err := vpn.cli.ContainerStop(ctx, routerContainerName, &timeout)
	if err != nil {
		errs = append(errs, err)
	}

	return nil
}
