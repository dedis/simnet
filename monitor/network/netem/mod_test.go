package main

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"go.dedis.ch/simnet/monitor/network"
)

func TestMain(t *testing.T) {
	file, err := ioutil.TempFile(os.TempDir(), "netem-test")
	require.NoError(t, err)

	defer file.Close()
	defer os.Remove(file.Name())

	os.Args = append(os.Args[:0], "--cmd", "echo", "--input", file.Name())

	enc := json.NewEncoder(file)
	err = enc.Encode(testMakeJSON())
	require.NoError(t, err)

	main()
}

func TestMain_BadInput(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expect a panic")
		} else {
			require.Equal(t, "couldn't decode: EOF", r.(error).Error())
		}
	}()

	file, err := ioutil.TempFile(os.TempDir(), "netem-test")
	require.NoError(t, err)

	defer file.Close()
	defer os.Remove(file.Name())

	os.Args = append(os.Args[:0], "--verbose", "--input", file.Name())

	// Nothing inside the file thus the decoding should fail..

	main()
}

func testMakeJSON() *[]network.RuleJSON {
	rules := make([]network.RuleJSON, 0)
	for _, r := range testMakeRules() {
		switch rule := r.(type) {
		case *network.DelayRule:
			rules = append(rules, network.RuleJSON{Delay: rule})
		case *network.LossRule:
			rules = append(rules, network.RuleJSON{Loss: rule})
		default:
			panic("not supported")
		}
	}

	return &rules
}
