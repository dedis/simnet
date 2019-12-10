package kubernetes

import (
	"bufio"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type vpn interface {
	Start() error
	Stop() error
}

type VpnOptions struct {
	Host   string
	Writer io.Writer
	CA     io.Reader
	Key    io.Reader
	Cert   io.Reader
}

type VpnOption func(opts *VpnOptions)

func WithHost(host string) VpnOption {
	return func(opts *VpnOptions) {
		opts.Host = host
	}
}

func WithWriter(w io.Writer) VpnOption {
	return func(opts *VpnOptions) {
		opts.Writer = w
	}
}

func WithCertificate(ca, key, cert io.Reader) VpnOption {
	return func(opts *VpnOptions) {
		opts.CA = ca
		opts.Key = key
		opts.Cert = cert
	}
}

// Ovpn is an implementation of the VPN interface that is using OpenVPN.
type Ovpn struct {
	cmd  *exec.Cmd
	dir  string
	host string
	out  io.Writer
}

// NewOvpn creates a new OpenVPN process.
func NewOvpn(opts ...VpnOption) (*Ovpn, error) {
	options := &VpnOptions{}
	for _, fn := range opts {
		fn(options)
	}

	dir, err := ioutil.TempDir(os.TempDir(), "simnet-vpn")
	if err != nil {
		return nil, err
	}

	err = writeFile(options.CA, "ca.crt", dir)
	if err != nil {
		return nil, err
	}

	err = writeFile(options.Key, "client1.key", dir)
	if err != nil {
		return nil, err
	}

	err = writeFile(options.Cert, "client1.crt", dir)
	if err != nil {
		return nil, err
	}

	return &Ovpn{
		host: options.Host,
		out:  options.Writer,
		dir:  dir,
	}, nil
}

// Start runs the openvpn process and returns any error that could happen before
// the tunnel is setup.
func (v *Ovpn) Start() error {
	args := []string{
		"openvpn",
		"--client",
		"--dev",
		"tun",
		"--ca",
		filepath.Join(v.dir, "ca.crt"),
		"--cert",
		filepath.Join(v.dir, "client1.crt"),
		"--key",
		filepath.Join(v.dir, "client1.key"),
		"--proto",
		"udp",
		"--remote",
		v.host,
		"--port",
		"1194",
		"--ns-cert-type", // TODO: deprecated
		"server",
		"--nobind",
		"--persist-key",
		"--persist-tun",
		"--comp-lzo",
		"--verb",
		"3",
	}

	r, w := io.Pipe()
	reader := bufio.NewScanner(r)

	cmd := exec.Command("sudo", args...)
	cmd.Stdout = w
	cmd.Stderr = w
	cmd.Stdin = os.Stdin

	v.cmd = cmd

	err := cmd.Start()
	if err != nil {
		return err
	}

	boot := make(chan struct{})

	go func() {
		for reader.Scan() {
			if strings.Contains(reader.Text(), "Initialization Sequence Completed") {
				close(boot)
			} else {
				// TODO: Write logs in a file.
				// fmt.Println(reader.Text())
			}
		}
	}()

	// TODO: timeout
	<-boot
	return nil
}

// Stop closes the vpn tunnel.
func (v *Ovpn) Stop() error {
	os.RemoveAll(v.dir)

	if v.cmd != nil {
		fmt.Fprintf(v.out, "Killing OpenVPN process %d\n", v.cmd.Process.Pid)
		killer := exec.Command("sudo", "killall", "openvpn")
		err := killer.Run()
		if err != nil {
			return err
		}
	}

	return nil
}

func writeFile(r io.Reader, name string, dir string) error {
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		return err
	}

	defer f.Close()

	br := bufio.NewReader(r)
	_, err = br.WriteTo(f)
	if err != nil {
		return err
	}

	return nil
}
