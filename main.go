package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/blang/semver/v4"
	"github.com/olekukonko/tablewriter"
	"golang.org/x/tools/benchmark/parse"
	"gopkg.in/src-d/go-git.v4"
	"gopkg.in/src-d/go-git.v4/plumbing"
	"gopkg.in/src-d/go-git.v4/plumbing/storer"
	"gopkg.in/yaml.v2"
	"k8s.io/klog/v2"
)

const (
	tagVersionPrefix = "v"
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
	flagConfiguration    = &BenchmarkConfiguration{}
	configPath           string
	benchmarks           = &BenchmarkList{}
	baseRef              string
	onlyRegression       bool
	compareLatestVersion bool
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
	flag.BoolVar(&compareLatestVersion, "compare-release", true, "compare with latest release version")
	flag.BoolVar(&onlyRegression, "only-regression", false, "")
}

func main() {
	flag.Parse()
	if err := run(); err != nil {
		klog.Fatal(err)
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

func versionRequired(required, tag string) bool {
	if required == "" {
		return true
	}
	required = strings.TrimSpace(required)
	tagVer, _ := semver.Make(trimTagVersion(tag))

	if strings.HasPrefix(required, ">=") {
		requiredVer, _ := semver.Make(trimTagVersion(strings.TrimLeft(required, ">=")))
		return tagVer.GTE(requiredVer)
	}
	if strings.HasPrefix(required, ">") {
		requiredVer, _ := semver.Make(trimTagVersion(strings.TrimLeft(required, ">")))
		return tagVer.GT(requiredVer)
	}
	requiredVer, _ := semver.Make(trimTagVersion(required))
	return tagVer.Equals(requiredVer)
}

func runBenchmarks(tagVersion string) (Set, error) {
	set := Set{}
	for i, benchmark := range benchmarks.Benchmarks {
		if tagVersion != "" && !versionRequired(benchmark.VersionRequirement, tagVersion) {
			klog.InfoS("Version required, skip test", "tagVersion", tagVersion, "versionRequirement", benchmark.VersionRequirement)
			continue
		}
		parseSet, err := runBenchmark(benchmarks.Command, &benchmarks.Benchmarks[i])
		if err != nil {
			klog.InfoS("Parse result error", "parseSet", parseSet)
			continue
		}
		if len(parseSet) != 1 {
			klog.InfoS("expected exactly one benchmark result", "got", parseSet)
			continue
		}
		if _, ok := set[benchmark.UniqueName]; ok {
			klog.InfoS("more than one benchmark with unique name", "Name", benchmark.UniqueName)
			continue
		}
		for name, s := range parseSet {
			if len(s) != 1 {
				klog.InfoS("expected exactly one benchmark result", "Name", name, "benchmark.UniqueName", benchmark.UniqueName)
				continue
			}
			set[benchmark.UniqueName] = s[0]
		}
	}
	return set, nil
}

func trimTagVersion(tagName string) string {
	return strings.TrimLeft(tagName, tagVersionPrefix)
}

func getLatestRelease(repository *git.Repository) (prevVersionTag *plumbing.Reference, err error) {
	var tagRefs storer.ReferenceIter
	tagRefs, err = repository.Tags()
	if err != nil {
		return
	}

	type SemverTag struct {
		Ref     *plumbing.Reference
		Version semver.Version
	}
	tags := make([]SemverTag, 0)
	err = tagRefs.ForEach(func(tagRef *plumbing.Reference) error {
		tagName := tagRef.Name().Short()
		v, err := semver.Make(trimTagVersion(tagName))
		if err != nil {
			klog.InfoS("Tag name is a not a valid semver, skipping", "tag", tagName, "err", err)
		}
		tags = append(tags, SemverTag{tagRef, v})
		return nil
	})
	sort.Slice(tags, func(i, j int) bool {
		t1 := &tags[i]
		t2 := &tags[j]
		return t1.Version.GT(t2.Version)
	})
	if len(tags) == 0 {
		return prevVersionTag, fmt.Errorf("version tags not found in repository")
	}
	prevVersionTag = tags[0].Ref
	klog.InfoS("Latest tag version", "tag", prevVersionTag)
	return
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

	resetAndRunBenchmark := func(commit plumbing.Hash, ref string, isTag bool) (benchSet Set, err error) {
		err = w.Reset(&git.ResetOptions{Commit: commit, Mode: git.HardReset})
		if err != nil {
			return nil, fmt.Errorf("failed to reset the worktree to a commit %v, ref %v: %w", commit, ref, err)
		}

		klog.InfoS("Run Benchmark", "commitHash", commit, "Ref", ref)
		var tagVersion string
		if isTag {
			tagVersion = ref
		}
		benchSet, err = runBenchmarks(tagVersion)
		if err != nil {
			return nil, fmt.Errorf("failed to run a benchmark: %w", err)
		}
		return
	}

	defer func() {
		_ = w.Reset(&git.ResetOptions{Commit: head.Hash(), Mode: git.HardReset})
	}()
	updateBenchmarks()

	// run benchmark of baseRef
	prevSet, err := resetAndRunBenchmark(*prev, baseRef, false)
	if err != nil {
		return err
	}

	// run benchmark of latestReleaseVersion
	var latestReleaseSet Set
	var tagName string
	var prevVersionTag *plumbing.Reference
	if compareLatestVersion {
		prevVersionTag, err = getLatestRelease(r)
		if err != nil {
			return fmt.Errorf("failed to get latest release version: %w", err)
		}
		tagName = prevVersionTag.Name().String()
		latestReleaseSet, err = resetAndRunBenchmark(prevVersionTag.Hash(), prevVersionTag.Name().Short(), true)
		if err != nil {
			return err
		}
	}

	// run benchmark of HEAD
	headSet, err := resetAndRunBenchmark(head.Hash(), "HEAD", false)
	if err != nil {
		return err
	}

	var ratios []result
	var rows [][]string
	var ratiosWithRelease []result

	for _, benchmark := range benchmarks.Benchmarks {
		benchName := benchmark.UniqueName
		headBench, ok := headSet[benchName]
		if !ok {
			klog.ErrorS(fmt.Errorf("missing benchmark '%s'", benchName), "missing benchmark", "benchName", benchName)
			continue
		}

		rows = append(rows, generateRow("HEAD", headBench))

		prevBench, ok := prevSet[benchName]
		if !ok {
			rows = append(rows, []string{benchName, baseRef, "-", "-"})
			continue
		}

		getRationsPerOP := func(headBench, baseBench *parse.Benchmark) (ratioNsPerOp float64) {
			if prevBench.NsPerOp != 0 {
				ratioNsPerOp = (headBench.NsPerOp - baseBench.NsPerOp) / baseBench.NsPerOp
			}
			return
		}

		getRatioAllocedBytesPerOp := func(headBench, baseBench *parse.Benchmark) (ratioAllocedBytesPerOp float64) {
			if prevBench.AllocedBytesPerOp != 0 {
				ratioAllocedBytesPerOp = (float64(headBench.AllocedBytesPerOp) - float64(baseBench.AllocedBytesPerOp)) / float64(baseBench.AllocedBytesPerOp)
			}
			return
		}

		rows = append(rows, generateRow(baseRef, prevBench))
		ratios = append(ratios, result{
			Benchmark:              benchmark,
			RatioNsPerOp:           getRationsPerOP(headBench, prevBench),
			RatioAllocedBytesPerOp: getRatioAllocedBytesPerOp(headBench, prevBench),
		})

		// get benchmark result of latestReleaseVersion
		if latestReleaseSet == nil {
			continue
		}
		if latestReleaseBench, ok := latestReleaseSet[benchName]; ok {
			rows = append(rows, generateRow(tagName, latestReleaseBench))
			ratiosWithRelease = append(ratiosWithRelease, result{
				Benchmark:              benchmark,
				RatioNsPerOp:           getRationsPerOP(headBench, latestReleaseBench),
				RatioAllocedBytesPerOp: getRatioAllocedBytesPerOp(headBench, latestReleaseBench),
			})
		}
	}

	if !onlyRegression {
		showResult(os.Stdout, rows)
	}

	regression := showRatio(os.Stdout, ratios, onlyRegression, baseRef)

	var regressionWithLatestVersion bool
	if latestReleaseSet != nil {
		regressionWithLatestVersion = showRatio(os.Stdout, ratiosWithRelease, onlyRegression, tagName)
	}
	if regression || regressionWithLatestVersion {
		return fmt.Errorf("this commit makes benchmarks worse，compared with %s: %t, compared with %s: %t",
			baseRef, regression, tagName, regressionWithLatestVersion)
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
		"-v",
	}
	if *benchmark.Benchmem {
		args = append(args, "-benchmem")
	}
	args = append(args, benchmark.Package)
	cmd := exec.Command(cmdStr, args...)
	cmd.Stderr = &stderr

	klog.InfoS("Running benchmark", "command", cmd)
	out, err := cmd.Output()
	if err != nil {
		if strings.HasSuffix(strings.TrimSpace(stderr.String()), "no packages to test") {
			return parse.Set{}, nil
		}
		klog.InfoS("Exec command output", "out", string(out))
		klog.InfoS("Exec command error", "err", stderr.String())
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

func showRatio(w io.Writer, results []result, onlyRegression bool, compareWith string) bool {
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
		fmt.Fprintln(w, fmt.Sprintf("\nComparison with %s", compareWith))
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
