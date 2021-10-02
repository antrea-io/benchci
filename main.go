package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"strings"

	"github.com/olekukonko/tablewriter"
	"golang.org/x/tools/benchmark/parse"
	git "gopkg.in/src-d/go-git.v4"
	"gopkg.in/src-d/go-git.v4/plumbing"
	"gopkg.in/yaml.v2"
)

type result struct {
	Benchmark
	RatioNsPerOp           float64
	RatioAllocedBytesPerOp float64
}

type comparedScore struct {
	nsPerOp           bool
	allocedBytesPerOp bool
}

var (
	flagConfiguration = &BenchmarkConfiguration{}
	configPath        string
	benchmarks        = &BenchmarkList{}
	baseRef           string
	onlyRegression    bool
)

type Set map[string]*parse.Benchmark

func init() {
	flagConfiguration.Benchmem = new(bool)
	flag.StringVar(&flagConfiguration.Benchtime, "benchtime", "1s", "")
	flag.Float64Var(&flagConfiguration.Threshold, "threshold", 0.2, "")
	flag.StringVar(&flagConfiguration.Compare, "compare", "ns/op,B/op", "")
	flag.StringVar(&flagConfiguration.Cpu, "cpu", "4", "")
	flag.StringVar(&flagConfiguration.Timeout, "timeout", "10m", "")
	flag.BoolVar(flagConfiguration.Benchmem, "benchmem", true, "")
	flag.StringVar(&configPath, "config", "", "")
	flag.StringVar(&baseRef, "base", "HEAD~1", "")
	flag.BoolVar(&onlyRegression, "only-regression", false, "")
}

func main() {
	flag.Parse()

	err := run()
	if err != nil {
		log.Fatal(err)
	}
}

func parseBenchmarks() error {
	data, err := ioutil.ReadFile(configPath)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(data, benchmarks)
}

func (c *BenchmarkConfiguration) applyDefaults(d *BenchmarkConfiguration) *BenchmarkConfiguration {
	if c.Benchtime == "" {
		c.Benchtime = d.Benchtime
	}
	if c.Threshold == 0 {
		c.Threshold = d.Threshold
	}
	if c.Compare == "" {
		c.Compare = d.Compare
	}
	if c.Cpu == "" {
		c.Cpu = d.Cpu
	}
	if c.Timeout == "" {
		c.Timeout = d.Timeout
	}
	if c.Benchmem == nil {
		c.Benchmem = d.Benchmem
	}
	return c
}

func updateBenchmarks() {
	for idx := range benchmarks.Benchmarks {
		benchmark := &benchmarks.Benchmarks[idx]
		if benchmark.UniqueName == "" {
			benchmark.UniqueName = benchmark.Name
		}
		benchmark.applyDefaults(&benchmarks.BenchmarkConfiguration).applyDefaults(flagConfiguration)
	}
}

func runBenchmarks() (Set, error) {
	set := Set{}
	for _, benchmark := range benchmarks.Benchmarks {
		parseSet, err := runBenchmark(benchmarks.Command, &benchmark)
		if err != nil {
			return nil, err
		}
		if len(parseSet) == 0 {
			continue
		}
		if len(parseSet) != 1 {
			return nil, fmt.Errorf("expected exactly one benchmark result")
		}
		if _, ok := set[benchmark.UniqueName]; ok {
			return nil, fmt.Errorf("more than one benchmark with unique name '%s'", benchmark.UniqueName)
		}
		for _, s := range parseSet {
			if len(s) != 1 {
				return nil, fmt.Errorf("expected exactly one benchmark result")
			}
			set[benchmark.UniqueName] = s[0]
		}
	}
	return set, nil
}

func run() error {
	if err := parseBenchmarks(); err != nil {
		return err
	}

	r, err := git.PlainOpen(".")
	if err != nil {
		return fmt.Errorf("unable to open the git repository: %w", err)
	}

	head, err := r.Head()
	if err != nil {
		return fmt.Errorf("unable to get the reference where HEAD is pointing to: %w", err)
	}

	prev, err := r.ResolveRevision(plumbing.Revision(baseRef))
	if err != nil {
		return fmt.Errorf("unable to resolves revision to corresponding hash: %w", err)
	}

	w, err := r.Worktree()
	if err != nil {
		return fmt.Errorf("unable to get a worktree based on the given fs: %w", err)
	}

	s, err := w.Status()
	if err != nil {
		return fmt.Errorf("unable to get the working tree status: %w", err)
	}

	if !s.IsClean() {
		return fmt.Errorf("the repository is dirty: commit all changes before running")
	}

	err = w.Reset(&git.ResetOptions{Commit: *prev, Mode: git.HardReset})
	if err != nil {
		return fmt.Errorf("failed to reset the worktree to a previous commit: %w", err)
	}

	defer func() {
		_ = w.Reset(&git.ResetOptions{Commit: head.Hash(), Mode: git.HardReset})
	}()

	updateBenchmarks()

	log.Printf("Run Benchmark: %s %s", prev, baseRef)
	prevSet, err := runBenchmarks()
	if err != nil {
		return fmt.Errorf("failed to run a benchmark: %w", err)
	}

	err = w.Reset(&git.ResetOptions{Commit: head.Hash(), Mode: git.HardReset})
	if err != nil {
		return fmt.Errorf("failed to reset the worktree to HEAD: %w", err)
	}

	log.Printf("Run Benchmark: %s %s", head.Hash(), "HEAD")
	headSet, err := runBenchmarks()
	if err != nil {
		return fmt.Errorf("failed to run a benchmark: %w", err)
	}

	var ratios []result
	var rows [][]string

	for _, benchmark := range benchmarks.Benchmarks {
		benchName := benchmark.UniqueName
		headBench, ok := headSet[benchName]
		if !ok {
			return fmt.Errorf("missing benchmark '%s'", benchName)
		}

		rows = append(rows, generateRow("HEAD", headBench))

		prevBench, ok := prevSet[benchName]
		if !ok {
			rows = append(rows, []string{benchName, baseRef, "-", "-"})
			continue
		}

		rows = append(rows, generateRow(baseRef, prevBench))

		var ratioNsPerOp float64
		if prevBench.NsPerOp != 0 {
			ratioNsPerOp = (headBench.NsPerOp - prevBench.NsPerOp) / prevBench.NsPerOp
		}

		var ratioAllocedBytesPerOp float64
		if prevBench.AllocedBytesPerOp != 0 {
			ratioAllocedBytesPerOp = (float64(headBench.AllocedBytesPerOp) - float64(prevBench.AllocedBytesPerOp)) / float64(prevBench.AllocedBytesPerOp)
		}

		ratios = append(ratios, result{
			Benchmark:              benchmark,
			RatioNsPerOp:           ratioNsPerOp,
			RatioAllocedBytesPerOp: ratioAllocedBytesPerOp,
		})
	}

	if !onlyRegression {
		showResult(os.Stdout, rows)
	}

	regression := showRatio(os.Stdout, ratios, onlyRegression)
	if regression {
		return fmt.Errorf("This commit makes benchmarks worse")
	}

	return nil
}

func runBenchmark(cmdStr string, benchmark *Benchmark) (parse.Set, error) {
	var stderr bytes.Buffer
	args := []string{
		"test",
		"-run", "'^$'",
		"-bench", benchmark.Name,
		"-benchtime", benchmark.Benchtime,
		"-timeout", benchmark.Timeout,
		"-cpu", benchmark.Cpu,
	}
	if *benchmark.Benchmem {
		args = append(args, "-benchmem")
	}
	args = append(args, benchmark.Package)
	cmd := exec.Command(cmdStr, args...)
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		if strings.HasSuffix(strings.TrimSpace(stderr.String()), "no packages to test") {
			return parse.Set{}, nil
		}
		log.Println(string(out))
		log.Println(stderr.String())
		return nil, fmt.Errorf("failed to run '%s' command: %w", cmd, err)
	}

	b := bytes.NewBuffer(out)
	s, err := parse.ParseSet(b)
	if err != nil {
		return nil, fmt.Errorf("failed to parse a result of benchmarks: %w", err)
	}
	return s, nil
}

func generateRow(ref string, b *parse.Benchmark) []string {
	return []string{b.Name, ref, fmt.Sprintf(" %.2f ns/op", b.NsPerOp),
		fmt.Sprintf(" %d B/op", b.AllocedBytesPerOp)}
}

func showResult(w io.Writer, rows [][]string) {
	fmt.Fprintln(w, "\nResult")
	fmt.Fprintf(w, "%s\n\n", strings.Repeat("=", 6))

	table := tablewriter.NewWriter(w)
	table.SetAutoFormatHeaders(false)
	table.SetAlignment(tablewriter.ALIGN_CENTER)
	headers := []string{"Name", "Commit", "NsPerOp", "AllocedBytesPerOp"}
	table.SetHeader(headers)
	table.SetAutoMergeCells(true)
	table.SetRowLine(true)
	table.AppendBulk(rows)
	table.Render()
}

func showRatio(w io.Writer, results []result, onlyRegression bool) bool {
	table := tablewriter.NewWriter(w)
	table.SetAutoFormatHeaders(false)
	table.SetAlignment(tablewriter.ALIGN_CENTER)
	table.SetRowLine(true)
	headers := []string{"Name", "NsPerOp", "AllocedBytesPerOp"}
	table.SetHeader(headers)

	var regression bool
	for _, result := range results {
		comparedScore := whichScoreToCompare(result.Compare)
		if comparedScore.nsPerOp && result.Threshold < result.RatioNsPerOp {
			regression = true
		} else if comparedScore.allocedBytesPerOp && result.Threshold < result.RatioAllocedBytesPerOp {
			regression = true
		} else {
			if onlyRegression {
				continue
			}
		}
		row := []string{result.Name, generateRatioItem(result.RatioNsPerOp), generateRatioItem(result.RatioAllocedBytesPerOp)}
		colors := []tablewriter.Colors{{}, generateColor(result.RatioNsPerOp), generateColor(result.RatioAllocedBytesPerOp)}
		if !comparedScore.nsPerOp {
			row[1] = "-"
			colors[1] = tablewriter.Colors{}
		}
		if !comparedScore.allocedBytesPerOp {
			row[2] = "-"
			colors[2] = tablewriter.Colors{}
		}
		table.Rich(row, colors)
	}
	if table.NumLines() > 0 {
		fmt.Fprintln(w, "\nComparison")
		fmt.Fprintf(w, "%s\n\n", strings.Repeat("=", 10))

		table.Render()
		fmt.Fprintln(w)
	}
	return regression
}

func generateRatioItem(ratio float64) string {
	if -0.0001 < ratio && ratio < 0.0001 {
		ratio = 0
	}
	if 0 <= ratio {
		return fmt.Sprintf("%.2f%%", 100*ratio)
	}
	return fmt.Sprintf("%.2f%%", -100*ratio)
}

func generateColor(ratio float64) tablewriter.Colors {
	if ratio > 0 {
		return tablewriter.Colors{tablewriter.Bold, tablewriter.FgHiRedColor}
	}
	return tablewriter.Colors{tablewriter.Bold, tablewriter.FgBlueColor}
}

func whichScoreToCompare(c string) comparedScore {
	var comparedScore comparedScore
	for _, cc := range strings.Split(c, ",") {
		switch cc {
		case "ns/op":
			comparedScore.nsPerOp = true
		case "B/op":
			comparedScore.allocedBytesPerOp = true
		}
	}
	return comparedScore
}
