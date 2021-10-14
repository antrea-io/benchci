package main

type BenchmarkConfiguration struct {
	Benchtime string  `yaml:"benchtime"`
	Threshold float64 `yaml:"threshold"`
	Compare   string  `yaml:"compare"`
	Cpu       string  `yaml:"cpu"`
	Timeout   string  `yaml:"timeout"`
	Benchmem  *bool   `yaml:"benchmem,omitempty"`
}

type Benchmark struct {
	Name                   string `yaml:"name"`
	Package                string `yaml:"package"`
	UniqueName             string `yaml:"uniqueName"`
	BenchmarkConfiguration `yaml:",inline"`
}

type BenchmarkList struct {
	BenchmarkConfiguration `yaml:",inline"`
	Command                string      `yaml:"command"`
	Benchmarks             []Benchmark `yaml:"benchmarks"`
}
