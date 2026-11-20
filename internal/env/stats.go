package env

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Sample is one live CPU and memory reading from a container runtime.
type Sample struct {
	CPUMillis   int   `json:"cpu_millis"`
	MemoryBytes int64 `json:"memory_bytes"`
}

// Sampler returns one live resource sample per running container handle.
// Missing handles are omitted; callers must not invent zeros.
type Sampler interface {
	Sample(ids ...string) (map[string]Sample, error)
}

func (c Container) Sample(ids ...string) (map[string]Sample, error) {
	s, ok := c.RT.(Sampler)
	if !ok {
		return map[string]Sample{}, nil
	}
	return s.Sample(ids...)
}

func (d DockerCLI) Sample(ids ...string) (map[string]Sample, error) {
	if len(ids) == 0 {
		return map[string]Sample{}, nil
	}
	args := append([]string{"stats", "--no-stream", "--format", "{{.Container}}\t{{.CPUPerc}}\t{{.MemUsage}}"}, ids...)
	out, err := d.run(args...)
	if err != nil {
		return nil, err
	}
	return parseDockerStats(string(out))
}

func parseDockerStats(out string) (map[string]Sample, error) {
	samples := map[string]Sample{}
	for line := range strings.SplitSeq(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		id, cpu, mem, ok := splitStatsLine(line)
		if !ok || cpu == "--" || strings.HasPrefix(mem, "--") {
			continue
		}
		cpuMillis, err := parseCPUMillis(cpu)
		if err != nil {
			continue
		}
		usage, _, _ := strings.Cut(mem, "/")
		memory, err := parseDockerBytes(strings.TrimSpace(usage))
		if err != nil {
			continue
		}
		samples[id] = Sample{CPUMillis: cpuMillis, MemoryBytes: memory}
	}
	return samples, nil
}

func splitStatsLine(line string) (id, cpu, mem string, ok bool) {
	parts := strings.SplitN(line, "\t", 3)
	if len(parts) != 3 {
		return "", "", "", false
	}
	id = strings.TrimSpace(parts[0])
	cpu = strings.TrimSpace(parts[1])
	mem = strings.TrimSpace(parts[2])
	if id == "" {
		return "", "", "", false
	}
	return id, cpu, mem, true
}

func parseCPUMillis(perc string) (int, error) {
	perc = strings.TrimSpace(strings.TrimSuffix(perc, "%"))
	f, err := strconv.ParseFloat(perc, 64)
	if err != nil || f < 0 {
		return 0, fmt.Errorf("env: cpu")
	}
	return int(math.Round(f * 10)), nil
}

func parseDockerBytes(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("env: memory")
	}
	i := 0
	for i < len(s) && (s[i] == '.' || s[i] >= '0' && s[i] <= '9') {
		i++
	}
	if i == 0 {
		return 0, fmt.Errorf("env: memory")
	}
	n, err := strconv.ParseFloat(s[:i], 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("env: memory")
	}
	unit := strings.ToLower(strings.TrimSpace(s[i:]))
	mult, ok := dockerByteUnit[unit]
	if !ok {
		return 0, fmt.Errorf("env: memory")
	}
	return int64(math.Round(n * float64(mult))), nil
}

// dockerByteUnit matches Docker RAM units: decimal and IEC prefixes are 1024.
var dockerByteUnit = map[string]int64{
	"b": 1, "": 1,
	"k": 1024, "kb": 1024, "ki": 1024, "kib": 1024,
	"m": 1 << 20, "mb": 1 << 20, "mi": 1 << 20, "mib": 1 << 20,
	"g": 1 << 30, "gb": 1 << 30, "gi": 1 << 30, "gib": 1 << 30,
	"t": 1 << 40, "tb": 1 << 40, "ti": 1 << 40, "tib": 1 << 40,
}
