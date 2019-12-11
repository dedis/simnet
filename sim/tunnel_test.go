package sim

import (
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

func TestTunnel_Start(t *testing.T) {
	dir, err := ioutil.TempDir(os.TempDir(), "simnet-vpn-test")
	require.NoError(t, err)

	defer os.RemoveAll(dir)

	tun := &DefaultTunnel{
		cmd:     exec.Command("echo"),
		host:    "abc",
		dir:     dir,
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

	err = tun.Start()
	require.NoError(t, err)
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
		dir: dir,
	}

	err = tun.Stop()
	require.NoError(t, err)

	_, err = cmd.Process.Wait()
	require.NoError(t, err)
}
