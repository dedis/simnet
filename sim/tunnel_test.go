package sim

import (
	"bytes"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTunnel_New(t *testing.T) {
	host := "abc"
	port := int32(1234)
	reader := new(bytes.Buffer)

	dir, err := ioutil.TempDir(os.TempDir(), "simnet-tunnel-test")
	defer os.RemoveAll(dir)

	tun, err := NewDefaultTunnel(
		WithOutput(dir),
		WithHost(host),
		WithPort(port),
		WithCertificate(reader, reader, reader),
	)

	require.NoError(t, err)
	require.NotNil(t, tun)
	require.Equal(t, tun.host, host)
	require.Equal(t, tun.port, fmt.Sprintf("%d", port))
}

func TestTunnel_NewFailures(t *testing.T) {
	r1 := new(bytes.Buffer)
	r2 := new(bytes.Buffer)
	r3 := new(bytes.Buffer)

	dir, err := ioutil.TempDir(os.TempDir(), "simnet-tunnel-test")
	defer os.RemoveAll(dir)

	_, err = NewDefaultTunnel(WithOutput(dir), WithCertificate(nil, r2, r3))
	require.Error(t, err)

	_, err = NewDefaultTunnel(WithOutput(dir), WithCertificate(r1, nil, r3))
	require.Error(t, err)

	_, err = NewDefaultTunnel(WithOutput(dir), WithCertificate(r1, r2, nil))
	require.Error(t, err)
}

func TestTunnel_WriteFileFailures(t *testing.T) {
	file, err := ioutil.TempFile(os.TempDir(), "simnet-tunnel-test")

	e := errors.New("oops")
	reader := badReader{err: e}
	err = writeFile(reader, file.Name(), "")
	require.Error(t, err)
	require.Equal(t, e, err)

	err = writeFile(reader, "", "")
	require.Error(t, err)
	require.IsType(t, (*os.PathError)(nil), err)
}

func TestTunnel_Start(t *testing.T) {
	dir, err := ioutil.TempDir(os.TempDir(), "simnet-vpn-test")
	require.NoError(t, err)

	defer os.RemoveAll(dir)

	tun := &DefaultTunnel{
		cmd:     exec.Command("echo"),
		host:    "abc",
		dir:     dir,
		logFile: filepath.Join(dir, LogFileName),
		timeout: 1 * time.Second,
	}

	file, err := os.Create(tun.logFile)
	require.NoError(t, err)

	defer file.Close()

	go func() {
		fmt.Fprintln(file, "A")
		fmt.Fprintln(file, "ABC")
		fmt.Fprintln(file, "D")

		time.Sleep(50 * time.Millisecond)

		fmt.Fprintln(file, MessageInitDone)
	}()

	err = tun.Start()
	require.NoError(t, err)
}

func TestTunnel_StartFailures(t *testing.T) {
	tun := &DefaultTunnel{}

	// Expect an error if the log file cannot be created.
	err := tun.Start()
	require.Error(t, err)
	require.IsType(t, (*os.PathError)(nil), err)

	file, err := ioutil.TempFile(os.TempDir(), "simnet-tunnel-test")
	require.NoError(t, err)
	file.Close()

	defer os.Remove(file.Name())

	// Expect an error if the command fails.
	tun = &DefaultTunnel{
		logFile: file.Name(),
		cmd:     exec.Command("definitely not a valid cmd"),
	}

	err = tun.Start()
	require.Error(t, err)
	require.Contains(t, err.Error(), "vpn initialization failed")

	// Expect an error if the tunnel connection times out.
	tun = &DefaultTunnel{
		logFile: file.Name(),
		cmd:     exec.Command("echo"),
		timeout: time.Duration(0),
	}

	err = tun.Start()
	require.Error(t, err)
	require.Contains(t, err.Error(), "timeout")
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
		dir:     dir,
		pidFile: filepath.Join(dir, PIDFileName),
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
		dir:     dir,
		pidFile: filepath.Join(dir, PIDFileName),
	}
	err = tun.Stop()
	require.Error(t, err)
	require.Equal(t, "operation not permitted", err.Error())
}

type badReader struct {
	err error
}

func (r badReader) Read([]byte) (int, error) {
	return 0, r.err
}
