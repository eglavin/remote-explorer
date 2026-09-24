// Command build cross-compiles remote-explorer for every supported platform.
//
//	go run ./scripts/build                                    # all targets into dist/
//	go run ./scripts/build -targets linux/amd64,windows/amd64 # a subset
//	go run ./scripts/build -version 1.2.0                     # override the stamped version
//
// Binaries are static (CGO_ENABLED=0) and named remote-explorer-<os>-<arch>,
// with a SHA256SUMS file alongside them.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

const binaryName = "remote-explorer"

var defaultTargets = []string{
	"windows/amd64", "windows/arm64",
	"darwin/amd64", "darwin/arm64",
	"linux/amd64", "linux/arm64",
}

type result struct {
	target, file string
	size         int64
	elapsed      time.Duration
	err          error
}

func main() {
	targetsFlag := flag.String("targets", strings.Join(defaultTargets, ","), "comma-separated GOOS/GOARCH pairs to build")
	outFlag := flag.String("out", "dist", "output directory, relative to the module root")
	versionFlag := flag.String("version", "", "version to stamp into the binaries (default: git describe)")
	flag.Parse()

	if err := run(*targetsFlag, *outFlag, *versionFlag); err != nil {
		fmt.Fprintln(os.Stderr, "build:", err)
		os.Exit(1)
	}
}

func run(targetList, outDir, version string) error {
	root, err := moduleRoot()
	if err != nil {
		return err
	}
	targets, err := parseTargets(targetList)
	if err != nil {
		return err
	}
	if version == "" {
		version = gitVersion(root)
	}
	if !filepath.IsAbs(outDir) {
		outDir = filepath.Join(root, outDir)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	// Only remove files this script produces, never the whole directory, in
	// case -out points somewhere that holds other files.
	if err := removeOldArtifacts(outDir); err != nil {
		return err
	}

	noun := "targets"
	if len(targets) == 1 {
		noun = "target"
	}
	fmt.Printf("Building %s %s for %d %s into %s\n\n", binaryName, version, len(targets), noun, outDir)
	results := make([]result, len(targets))
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = build(root, outDir, t, version)
		}()
	}
	wg.Wait()

	failed := 0
	for _, r := range results {
		if r.err != nil {
			failed++
			fmt.Printf("  FAIL  %-14s %v\n", r.target, r.err)
			continue
		}
		fmt.Printf("  ok    %-14s %-36s %6.1f MB  %4.1fs\n",
			r.target, r.file, float64(r.size)/(1<<20), r.elapsed.Seconds())
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d targets failed", failed, len(targets))
	}

	if err := writeChecksums(outDir, results); err != nil {
		return err
	}
	fmt.Printf("\n  wrote SHA256SUMS\n")
	return nil
}

func parseTargets(list string) ([]string, error) {
	var targets []string
	for _, t := range strings.Split(list, ",") {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		goos, goarch, ok := strings.Cut(t, "/")
		if !ok || goos == "" || goarch == "" || strings.Contains(goarch, "/") {
			return nil, fmt.Errorf("invalid target %q, want GOOS/GOARCH such as linux/amd64", t)
		}
		if !slices.Contains(targets, t) {
			targets = append(targets, t)
		}
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("no targets given")
	}
	return targets, nil
}

func build(root, outDir, target, version string) result {
	start := time.Now()
	goos, goarch, _ := strings.Cut(target, "/")
	file := fmt.Sprintf("%s-%s-%s", binaryName, goos, goarch)
	if goos == "windows" {
		file += ".exe"
	}
	r := result{target: target, file: file}

	cmd := exec.Command("go", "build",
		"-trimpath",
		"-ldflags", "-s -w -X main.version="+version,
		"-o", filepath.Join(outDir, file),
		"./cmd/"+binaryName,
	)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		r.err = fmt.Errorf("%w\n%s", err, strings.TrimSpace(stderr.String()))
		return r
	}

	info, err := os.Stat(filepath.Join(outDir, file))
	if err != nil {
		r.err = err
		return r
	}
	r.size, r.elapsed = info.Size(), time.Since(start)
	return r
}

func writeChecksums(outDir string, results []result) error {
	var lines []string
	for _, r := range results {
		sum, err := sha256File(filepath.Join(outDir, r.file))
		if err != nil {
			return err
		}
		// Two spaces between hash and name is the format sha256sum -c expects.
		lines = append(lines, sum+"  "+r.file)
	}
	slices.SortFunc(lines, func(a, b string) int { return strings.Compare(a[66:], b[66:]) })
	return os.WriteFile(filepath.Join(outDir, "SHA256SUMS"), []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func removeOldArtifacts(outDir string) error {
	entries, err := os.ReadDir(outDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() == "SHA256SUMS" || strings.HasPrefix(e.Name(), binaryName+"-") {
			if err := os.Remove(filepath.Join(outDir, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

// moduleRoot lets the script run from any directory inside the module.
func moduleRoot() (string, error) {
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		return "", fmt.Errorf("go env GOMOD: %w", err)
	}
	gomod := strings.TrimSpace(string(out))
	if gomod == "" || gomod == os.DevNull {
		return "", fmt.Errorf("run this from inside the %s module", binaryName)
	}
	return filepath.Dir(gomod), nil
}

// gitVersion describes the checkout, e.g. "v1.2.0", "v1.2.0-3-g39f0bbe" or
// "39f0bbe-dirty", falling back to "dev" outside a git repository.
func gitVersion(root string) string {
	cmd := exec.Command("git", "describe", "--tags", "--always", "--dirty")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return "dev"
	}
	return strings.TrimSpace(string(out))
}
