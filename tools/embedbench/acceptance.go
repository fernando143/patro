package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/fernando143/patro/internal/adapter/embed"
)

const measuredRuns = 30

type corpusManifest struct {
	ID          string            `json:"id"`
	SHA256      string            `json:"sha256"`
	Seed        int64             `json:"seed"`
	Dimension   int               `json:"dimension"`
	Cases       []manifestCase    `json:"cases"`
	Calibration []calibrationCase `json:"calibration"`
}

type manifestCase struct {
	ID    string `json:"id"`
	A     string `json:"a"`
	B     string `json:"b"`
	APath string `json:"a_path"`
	BPath string `json:"b_path"`
}

type runStats struct {
	P50       time.Duration
	P95       time.Duration
	MeanAlloc uint64
}

func runAcceptance(path string) int {
	manifest, err := loadManifest(path)
	if err != nil {
		fmt.Printf("acceptance: authoritative=false pass=false error=%v\n", err)
		return 1
	}
	authoritative, reason := authoritativeHost()
	if !authoritative {
		fmt.Printf("acceptance: authoritative=false pass=false reason=%s\n", reason)
		return 1
	}
	if len(embed.Available()) == 0 {
		fmt.Println("acceptance: authoritative=false pass=false reason=no embedding backend")
		return 1
	}
	backend, err := embed.New(embed.Available()[0])
	if err != nil {
		fmt.Printf("acceptance: authoritative=true pass=false error=%v\n", err)
		return 1
	}
	for _, c := range manifest.Cases {
		a, err := caseText(path, c.A, c.APath)
		if err != nil {
			fmt.Printf("acceptance: authoritative=true pass=false error=%v\n", err)
			return 1
		}
		b, err := caseText(path, c.B, c.BPath)
		if err != nil {
			fmt.Printf("acceptance: authoritative=true pass=false error=%v\n", err)
			return 1
		}
		if _, err := backend.Represent(context.Background(), embed.Document{ID: c.ID + "-a", Text: a}); err != nil {
			fmt.Printf("acceptance: authoritative=true pass=false error=%v\n", err)
			return 1
		}
		if _, err := backend.Represent(context.Background(), embed.Document{ID: c.ID + "-b", Text: b}); err != nil {
			fmt.Printf("acceptance: authoritative=true pass=false error=%v\n", err)
			return 1
		}
	}
	// The benchmark deliberately keeps the gate small and deterministic: the
	// measured block is exactly five warmups followed by thirty runs.  The
	// emitted report is timestamp-free; wall-clock values are only gate data.
	stats, err := measure(context.Background(), func(ctx context.Context) error {
		a, err := caseText(path, manifest.Cases[0].A, manifest.Cases[0].APath)
		if err != nil {
			return err
		}
		_, err = backend.Represent(ctx, embed.Document{ID: "acceptance", Text: a})
		return err
	})
	if err != nil {
		fmt.Printf("acceptance: authoritative=true pass=false error=%v\n", err)
		return 1
	}
	pass := stats.P95 <= 100*time.Millisecond
	fmt.Printf("acceptance: authoritative=true pass=%t backend=%s p50=%s p95=%s mean_alloc=%d\n", pass, backend.Name(), stats.P50, stats.P95, stats.MeanAlloc)
	if !pass {
		return 1
	}
	return 0
}

func caseText(manifestPath, inline, relative string) (string, error) {
	if relative == "" {
		return inline, nil
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(manifestPath), relative))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func loadManifest(path string) (corpusManifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return corpusManifest{}, err
	}
	var manifest corpusManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return corpusManifest{}, err
	}
	if manifest.ID == "" || len(manifest.Cases) == 0 || manifest.Dimension <= 0 {
		return corpusManifest{}, fmt.Errorf("invalid corpus manifest")
	}
	if manifest.SHA256 != "" {
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != manifest.SHA256 {
			// The manifest hash is self-referential in a single file, so this
			// check is intentionally only used when a detached hash is supplied.
			return corpusManifest{}, fmt.Errorf("corpus manifest sha256 mismatch")
		}
	}
	return manifest, nil
}

func authoritativeHost() (bool, string) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || !strings.HasPrefix(runtime.Version(), "go1.26.5") {
		return false, fmt.Sprintf("requires linux/amd64 Go 1.26.5, got %s/%s %s", runtime.GOOS, runtime.GOARCH, runtime.Version())
	}
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil || !strings.Contains(strings.ToLower(string(data)), "7800x3d") {
		return false, "requires Ryzen 7 7800X3D host"
	}
	gov, err := os.ReadFile("/sys/devices/system/cpu/cpu0/cpufreq/scaling_governor")
	if err != nil || strings.TrimSpace(string(gov)) != "performance" {
		return false, "CPU governor is not performance"
	}
	return true, ""
}

func measure(ctx context.Context, fn func(context.Context) error) (runStats, error) {
	for i := 0; i < 5; i++ {
		if err := fn(ctx); err != nil {
			return runStats{}, err
		}
	}
	runs := make([]time.Duration, 0, measuredRuns)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	var totalAlloc uint64
	for i := 0; i < measuredRuns; i++ {
		runtime.ReadMemStats(&before)
		start := time.Now()
		if err := fn(ctx); err != nil {
			return runStats{}, err
		}
		runs = append(runs, time.Since(start))
		runtime.ReadMemStats(&after)
		totalAlloc += after.TotalAlloc - before.TotalAlloc
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i] < runs[j] })
	return runStats{P50: runs[nearestRank(.50)], P95: runs[nearestRank(.95)], MeanAlloc: totalAlloc / measuredRuns}, nil
}

func nearestRank(p float64) int {
	return int(math.Ceil(p*measuredRuns)) - 1
}
