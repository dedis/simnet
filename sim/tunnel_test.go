package sim

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTunnel_Start(t *testing.T) {
	dir, err := ioutil.TempDir(os.TempDir(), "simnet-vpn-test")
	require.NoError(t, err)

	defer os.RemoveAll(dir)

	tun := &DefaultTunnel{
		cmd:     exec.Command("echo"),
		outDir:  dir,
		timeout: 1 * time.Second,
	}

	file, err := os.Create(filepath.Join(dir, LogFileName))
	require.NoError(t, err)

	defer file.Close()

	go func() {
		fmt.Fprintln(file, "A")
		fmt.Fprintln(file, "ABC")
		fmt.Fprintln(file, "D")

		time.Sleep(50 * time.Millisecond)

		fmt.Fprintln(file, MessageInitDone)
	}()

	certs := Certificates{}

	err = tun.Start(WithPort(1234), WithHost("1.2.3.4"), WithCertificate(certs))
	require.NoError(t, err)
	require.Contains(t, tun.cmd.Args, "1234")
	require.Contains(t, tun.cmd.Args, "1.2.3.4")
}

func TestTunnel_StartFailures(t *testing.T) {
	tun := NewDefaultTunnel("/")

	// Expect an error if the log file cannot be created.
	err := tun.Start()
	require.Error(t, err)
	require.IsType(t, (*os.PathError)(nil), err)

	dir, err := ioutil.TempDir(os.TempDir(), "simnet-tunnel-test")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	// Expect an error if the command fails.
	tun = &DefaultTunnel{
		outDir: dir,
		cmd:    exec.Command("definitely not a valid cmd"),
	}

	err = tun.Start()
	require.Error(t, err)
	require.Contains(t, err.Error(), "vpn initialization failed")

	// Expect an error if the tunnel connection times out.
	tun = &DefaultTunnel{
		outDir:  dir,
		cmd:     exec.Command("echo"),
		timeout: time.Duration(0),
	}

	err = tun.Start()
	require.Error(t, err)
	require.True(t, errors.Is(err, errTunnelTimeout))

	// Expect an error if the current user cannot be read.
	e := errors.New("current user error")
	setMockBadCurrentUser(e)
	defer setCurrentUser()
	err = tun.Start()
	require.Error(t, err)
	require.True(t, errors.Is(err, e))
}

func TestTunnel_Stop(t *testing.T) {
	dir, err := ioutil.TempDir(os.TempDir(), "simnet-vpn-test")
	require.NoError(t, err)

	cmd := exec.Command("cat")
	require.NoError(t, cmd.Start())
	pid := strconv.Itoa(cmd.Process.Pid)

	err = ioutil.WriteFile(filepath.Join(dir, PIDFileName), []byte(pid), 0644)
	require.NoError(t, err)

	defer os.RemoveAll(dir)

	tun := &DefaultTunnel{
		outDir: dir,
	}

	err = tun.Stop()
	require.NoError(t, err)

	_, err = cmd.Process.Wait()
	require.NoError(t, err)
}

func TestTunnel_StopFailures(t *testing.T) {
	tun := &DefaultTunnel{}

	// Expect error when the pid file does not exist.
	err := tun.Stop()
	require.Error(t, err)
	require.IsType(t, (*os.PathError)(nil), err)

	// Expect error for a process that cannot be killed.
	dir, err := ioutil.TempDir(os.TempDir(), "simnet-vpn-test")
	require.NoError(t, err)

	err = ioutil.WriteFile(filepath.Join(dir, PIDFileName), []byte("1"), 0644)
	require.NoError(t, err)

	tun = &DefaultTunnel{
		outDir: dir,
	}
	err = tun.Stop()
	require.Error(t, err)
	require.Equal(t, "operation not permitted", err.Error())

	e := errors.New("find process error")
	setMockBadFindProcess(e)
	defer setFindProcess()
	err = tun.Stop()
	require.Error(t, err)
	require.True(t, errors.Is(err, e))
}

type badReader struct {
	err error
}

func (r badReader) Read([]byte) (int, error) {
	return 0, r.err
}

func setCurrentUser() {
	currentUser = user.Current
}

func setMockBadCurrentUser(err error) {
	currentUser = func() (*user.User, error) {
		return nil, err
	}
}

func setFindProcess() {
	findProcess = os.FindProcess
}

func setMockBadFindProcess(err error) {
	findProcess = func(int) (*os.Process, error) {
		return nil, err
	}
}
