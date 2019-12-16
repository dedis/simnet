package sim

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// VpnConnectionTimeout is the maximum amount of time given to open the
	// VPN tunnel.
	VpnConnectionTimeout = 10 * time.Second

	// LogFileName is the name of the file written in the temporary folder
	// where the logs of the vpn are written.
	LogFileName = "out.log"
	// PIDFileName is the name of the file written in the temporary folder
	// containing the PID of the VPN process.
	PIDFileName = "running_pid"

	// MessageInitDone is the message to look for to assume the tunnel is
	// opened.
	MessageInitDone = "Initialization Sequence Completed"

	// FileNameCA is the name of the file where the public key of the CA will
	// be written.
	FileNameCA = "ca.crt"
	// FileNameCert is the name of the file where the public of the client
	// will be written.
	FileNameCert = "client.crt"
	// FileNameKey is the name of the file where the private key of the client
	// will be written.
	FileNameKey = "client.key"
)

// Tunnel provides primitives to open and close a tunnel to a private network.
type Tunnel interface {
	Start() error
	Stop() error
}

// TunOptions contains the data that will be used to start the vpn.
type TunOptions struct {
	Host string
	CA   io.Reader
	Key  io.Reader
	Cert io.Reader
}

// TunOption is a function that transforms the vpn options.
type TunOption func(opts *TunOptions)

// WithHost updates the options to include the hostname of the distant vpn.
func WithHost(host string) TunOption {
	return func(opts *TunOptions) {
		opts.Host = host
	}
}

// WithCertificate updates the options to include the certificate elements
// in the parameters used to start the vpn.
func WithCertificate(ca, key, cert io.Reader) TunOption {
	return func(opts *TunOptions) {
		opts.CA = ca
		opts.Key = key
		opts.Cert = cert
	}
}

// DefaultTunnel is an implementation of the tunnel interface that is using OpenVPN.
type DefaultTunnel struct {
	cmd     *exec.Cmd
	dir     string
	logFile string
	pidFile string
	host    string
	in      io.ReadCloser
	timeout time.Duration
}

// NewDefaultTunnel creates a new OpenVPN process.
func NewDefaultTunnel(opts ...TunOption) (*DefaultTunnel, error) {
	options := &TunOptions{}
	for _, fn := range opts {
		fn(options)
	}

	dir, err := ioutil.TempDir(os.TempDir(), "simnet-vpn")
	if err != nil {
		return nil, err
	}

	err = writeFile(options.CA, FileNameCA, dir)
	if err != nil {
		return nil, err
	}

	err = writeFile(options.Key, FileNameKey, dir)
	if err != nil {
		return nil, err
	}

	err = writeFile(options.Cert, FileNameCert, dir)
	if err != nil {
		return nil, err
	}

	cmd := exec.Command("sudo")

	return &DefaultTunnel{
		cmd:     cmd,
		host:    options.Host,
		dir:     dir,
		logFile: filepath.Join(dir, LogFileName),
		pidFile: filepath.Join(dir, PIDFileName),
		timeout: VpnConnectionTimeout,
	}, nil
}

// Start runs the openvpn process and returns any error that could happen before
// the tunnel is setup.
func (v *DefaultTunnel) Start() error {
	file, err := os.Create(v.logFile)
	if err != nil {
		return err
	}

	defer file.Close()

	usr, err := user.Current()
	if err != nil {
		return err
	}

	args := []string{
		"openvpn",
		"--client",
		"--dev",
		"tun",
		"--ca",
		filepath.Join(v.dir, FileNameCA),
		"--cert",
		filepath.Join(v.dir, FileNameCert),
		"--key",
		filepath.Join(v.dir, FileNameKey),
		"--proto",
		"udp",
		"--remote",
		v.host,
		"--port",
		"1194",
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
		v.logFile,
		"--machine-readable-output",
		"--writepid",
		v.pidFile,
		"--verb",
		"3",
	}

	cmd := v.cmd
	cmd.Args = append(cmd.Args[:1], args...)

	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("vpn initialization failed: see %s", v.logFile)
	}

	timeout := time.After(v.timeout)

	for {
		select {
		case <-timeout:
			return errors.New("tunnel timeout")
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
	defer os.RemoveAll(v.dir)

	file, err := os.Open(v.pidFile)
	if err != nil {
		return err
	}

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		pid, err := strconv.Atoi(scanner.Text())
		if err == nil {
			proc, err := os.FindProcess(pid)
			if err != nil {
				return err
			}

			err = proc.Kill()
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func writeFile(r io.Reader, name string, dir string) error {
	if r == nil {
		return errors.New("missing certificate reader")
	}

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
