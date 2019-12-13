package main

import (
	"bufio"
	"bytes"
	"io/ioutil"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.dedis.ch/simnet/monitor/network"
)

func TestExecutor_Execute(t *testing.T) {
	out := new(bytes.Buffer)
	exec := executor{
		dev: "eth0",
		cmd: "echo",
		out: out,
	}

	err := exec.Execute(testMakeRules())
	require.NoError(t, err)

	scanner := bufio.NewScanner(out)
	for _, cmd := range testExpectedCommands {
		require.True(t, scanner.Scan()) // ignore the log
		require.True(t, scanner.Scan())
		require.Equal(t, cmd, scanner.Text())
	}
}

func TestExecutor_ExecuteFailure(t *testing.T) {
	exec := executor{
		dev: "eth0",
		cmd: "definitely not a valid command",
		out: ioutil.Discard,
	}

	err := exec.Execute(testMakeRules())
	require.Error(t, err)
	require.Contains(t, err.Error(), " command failed:")
}

func TestExecutor_MakeNetEm(t *testing.T) {
	str := makeNetEm(network.NewDelayRule("1.2.3.4", time.Millisecond))
	require.Equal(t, "netem delay 1ms", str)

	str = makeNetEm(&network.SingleAddrRule{})
	require.Equal(t, "", str)
}

var testExpectedCommands = []string{
	"qdisc add dev eth0 root handle 1: htb",
	"class add dev eth0 parent 1: classid 1:1 htb rate 1000Mbps",
	// First ip with one qdisc
	"class add dev eth0 parent 1:1 classid 1:10 htb rate 1000Mbps",
	"filter add dev eth0 protocol ip parent 1:0 prio 1 u32 match ip dst 127.0.0.1/32 flowid 1:10",
	"qdisc add dev eth0 parent 1:10 handle 11: netem delay 1000ms",
	// Second with two qdisc
	"class add dev eth0 parent 1:1 classid 1:20 htb rate 1000Mbps",
	"filter add dev eth0 protocol ip parent 1:0 prio 1 u32 match ip dst 127.0.0.2/32 flowid 1:20",
	"qdisc add dev eth0 parent 1:20 handle 21: netem delay 1000ms",
	"qdisc add dev eth0 parent 21: handle 22: netem delay 1000ms",
	// Third with again one qdisc.
	"class add dev eth0 parent 1:1 classid 1:30 htb rate 1000Mbps",
	"filter add dev eth0 protocol ip parent 1:0 prio 1 u32 match ip dst 127.0.0.3/32 flowid 1:30",
	"qdisc add dev eth0 parent 1:30 handle 31: netem loss 50%",
	// Fourth with one qdisc.
	"class add dev eth0 parent 1:1 classid 1:40 htb rate 1000Mbps",
	"filter add dev eth0 protocol ip parent 1:0 prio 1 u32 match ip dst 127.0.0.4/32 flowid 1:40",
	"qdisc add dev eth0 parent 1:40 handle 41: netem delay 1000ms",
}

func testMakeRules() []network.Rule {
	return []network.Rule{
		network.NewDelayRule("127.0.0.1", time.Second),
		network.NewDelayRule("127.0.0.2", time.Second),
		network.NewDelayRule("127.0.0.2", time.Second),
		network.NewLossRule("127.0.0.3", 0.5),
		network.NewDelayRule("127.0.0.4", time.Second),
	}
}
