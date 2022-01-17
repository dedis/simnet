package sim

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/xerrors"
)

const (
	// VpnConnectionTimeout is the maximum amount of time given to open the
	// VPN tunnel.
	VpnConnectionTimeout = 10 * time.Second

	// LogFileName is the name of the file written in the temporary folder
	// where the logs of the vpn are written.
	LogFileName = "vpn.log"
	// PIDFileName is the name of the file written in the temporary folder
	// containing the PID of the VPN process.
	PIDFileName = "running_pid"

	// MessageInitDone is the message to look for to assume the tunnel is
	// opened.
	MessageInitDone = "Initialization Sequence Completed"
)

// Defines package functions from the standard library to enable unit testing.
var currentUser = user.Current
var findProcess = os.FindProcess

var errTunnelTimeout = errors.New("tunnel timeout")

// Tunnel provides primitives to open and close a tunnel to a private network.
type Tunnel interface {
	Start(...TunOption) error
	Stop() error
}

// Certificates holds the location of the different files.
type Certificates struct {
	CA   string
	Key  string
	Cert string
}

// TunOptions contains the data that will be used to start the vpn.
type TunOptions struct {
	Cmd          string
	Host         string
	Port         int32
	Certificates Certificates
}

// TunOption is a function that transforms the vpn options.
type TunOption func(opts *TunOptions)

// WithCommand updates the options to use a different executable.
func WithCommand(cmd string) TunOption {
	return func(opts *TunOptions) {
		opts.Cmd = cmd
	}
}

// WithHost updates the options to include the hostname of the distant vpn.
func WithHost(host string) TunOption {
	return func(opts *TunOptions) {
		opts.Host = host
	}
}

// WithPort updates the options to include the port of the distant vpn.
func WithPort(port int32) TunOption {
	return func(opts *TunOptions) {
		opts.Port = port
	}
}

// WithCertificate updates the options to include the certificate elements
// in the parameters used to start the vpn.
func WithCertificate(certs Certificates) TunOption {
	return func(opts *TunOptions) {
		opts.Certificates = certs
	}
}

// DefaultTunnel is an implementation of the tunnel interface that is using OpenVPN.
type DefaultTunnel struct {
	cmd     *exec.Cmd
	outDir  string
	in      io.ReadCloser
	timeout time.Duration
}

// NewDefaultTunnel creates a new OpenVPN process.
func NewDefaultTunnel(output string) *DefaultTunnel {
	cmd := exec.Command("sudo")

	return &DefaultTunnel{
		cmd:     cmd,
		outDir:  output,
		timeout: VpnConnectionTimeout,
	}
}

// Start runs the openvpn process and returns any error that could happen before
// the tunnel is setup.
func (v *DefaultTunnel) Start(opts ...TunOption) error {
	options := &TunOptions{Cmd: "openvpn"}
	for _, fn := range opts {
		fn(options)
	}

	file, err := os.Create(filepath.Join(v.outDir, LogFileName))
	if err != nil {
		return xerrors.Errorf("failed creating VPN log: %w", err)
	}

	defer file.Close()

	usr, err := currentUser()
	if err != nil {
		return xerrors.Errorf("failed getting current user: %w", err)
	}

	args := []string{
		options.Cmd,
		"--client",
		"--dev",
		"tun",
		"--ca",
		options.Certificates.CA,
		"--cert",
		options.Certificates.Cert,
		"--key",
		options.Certificates.Key,
		"--proto",
		"udp",
		"--remote",
		options.Host,
		"--port",
		fmt.Sprintf("%d", options.Port),
		"--remote-cert-tls",
		"server",
		"--nobind",
		"--persist-key",
		"--persist-tun",
		"--comp-lzo",
		"--user",
		usr.Username,
		"--daemon",
		"--log",
		filepath.Join(v.outDir, LogFileName),
		"--machine-readable-output",
		"--writepid",
		filepath.Join(v.outDir, PIDFileName),
		"--verb",
		"3",
	}

	cmd := v.cmd
	cmd.Args = append(cmd.Args[:1], args...)

	err = cmd.Run()
	if err != nil {
		fmt.Printf("Command 'sudo %s' returns error: %s\n", options.Cmd, err)
		return fmt.Errorf("vpn initialization failed: see %s", filepath.Join(v.outDir, LogFileName))
	}

	timeout := time.After(v.timeout)

	for {
		select {
		case <-timeout:
			return errTunnelTimeout
		default:
			scanner := bufio.NewScanner(file)
			for scanner.Scan() {
				if strings.Contains(scanner.Text(), MessageInitDone) {
					return nil
				}
			}
		}

		time.Sleep(5 * time.Millisecond)
	}
}

// Stop closes the vpn tunnel.
func (v *DefaultTunnel) Stop() error {
	file, err := os.Open(filepath.Join(v.outDir, PIDFileName))
	if err != nil {
		return xerrors.Errorf("couldn't read PID: %v", err)
	}

	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		pid, err := strconv.Atoi(scanner.Text())
		if err == nil {
			proc, err := findProcess(pid)
			if err != nil {
				return xerrors.Errorf("failed to find process: %w", err)
			}

			err = proc.Kill()
			if err != nil {
				if err.Error() == "os: process already finished" {
					return nil
				}
				return xerrors.Errorf("failed to kill: %w", err)
			}
		}
	}

	return nil
}
