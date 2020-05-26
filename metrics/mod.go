package metrics

import (
	"bufio"
	"io"
	"strconv"
	"strings"
	"time"

	"gonum.org/v1/gonum/stat"
)

// Stats represents the JSON structure of the statistics written for each node.
type Stats struct {
	Timestamp int64
	Tags      map[int64]string
	Nodes     map[string]NodeStats
}

// NewStats returns a new instance of a statistics object.
func NewStats() Stats {
	return Stats{
		Tags:  make(map[int64]string),
		Nodes: make(map[string]NodeStats),
	}
}

// NodeStats contains the array of data that represents a timeline of the
// resource usage of a node.
type NodeStats struct {
	Timestamps []int64
	RxBytes    []uint64
	TxBytes    []uint64
	CPU        []uint64
	Memory     []uint64
}

// NewNodeStats creates statistics for a node by reading the reader line by line.
func NewNodeStats(reader io.Reader, start, end time.Time) NodeStats {
	ns := NodeStats{}

	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()
		numbers := parseLine(line)

		if len(numbers) >= 5 {
			ts := int64(numbers[0])

			if ts >= start.Unix() && ts <= end.Unix() {
				ns.Timestamps = append(ns.Timestamps, ts)
				ns.RxBytes = append(ns.RxBytes, numbers[1])
				ns.TxBytes = append(ns.TxBytes, numbers[2])
				ns.CPU = append(ns.CPU, numbers[3])
				ns.Memory = append(ns.Memory, numbers[4])
			}
		}
	}

	return ns
}

// Max returns the maximum value for each column.
func (ns NodeStats) Max() (uint64, uint64, uint64, uint64) {
	cpu := uint64(0)
	mem := uint64(0)
	tx := uint64(0)
	rx := uint64(0)

	for i := range ns.Timestamps {
		if cpu < ns.CPU[i] {
			cpu = ns.CPU[i]
		}

		if mem < ns.Memory[i] {
			mem = ns.Memory[i]
		}

		if tx < ns.TxBytes[i] {
			tx = ns.TxBytes[i]
		}

		if rx < ns.RxBytes[i] {
			rx = ns.RxBytes[i]
		}
	}

	return cpu, mem, tx, rx
}

// Average returns the average for each column.
func (ns NodeStats) Average() (float64, float64, float64, float64) {
	cpu := uint64(0)
	mem := uint64(0)
	tx := uint64(0)
	rx := uint64(0)

	for i := range ns.Timestamps {
		cpu += ns.CPU[i]
		mem += ns.Memory[i]
		tx += ns.TxBytes[i]
		rx += ns.RxBytes[i]
	}

	length := float64(len(ns.Timestamps))
	acpu := float64(cpu) / length
	amem := float64(mem) / length
	atx := float64(tx) / length
	arx := float64(rx) / length

	return acpu, amem, atx, arx
}

// StdDev returns the standard deviation for each column.
func (ns NodeStats) StdDev() (float64, float64, float64, float64) {
	cpu := make([]float64, len(ns.Timestamps))
	mem := make([]float64, len(ns.Timestamps))
	tx := make([]float64, len(ns.Timestamps))
	rx := make([]float64, len(ns.Timestamps))

	for i := range ns.Timestamps {
		cpu[i] = float64(ns.CPU[i])
		mem[i] = float64(ns.Memory[i])
		tx[i] = float64(ns.TxBytes[i])
		rx[i] = float64(ns.RxBytes[i])
	}

	stddevcpu := stat.StdDev(cpu, nil)
	stddevmem := stat.StdDev(mem, nil)
	stddevtx := stat.StdDev(tx, nil)
	stddevrx := stat.StdDev(rx, nil)

	return stddevcpu, stddevmem, stddevtx, stddevrx
}

func parseInteger(value string) (uint64, error) {
	return strconv.ParseUint(strings.Trim(value, " "), 10, 64)
}

// parseLine reads a string and tries to convert values delimited by a
// comma. The returned array is filled on success and left empty if none.
func parseLine(line string) []uint64 {
	values := strings.Split(line, ",")
	numbers := make([]uint64, 0, len(values))

	for _, value := range values {
		num, err := parseInteger(value)
		if err == nil {
			numbers = append(numbers, num)
		}
	}

	return numbers
}
