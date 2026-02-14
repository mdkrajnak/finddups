package scanner

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/cespare/xxhash/v2"
	"github.com/mike/finddups/db"
	"github.com/mike/finddups/model"
)

const (
	partialSize = 8192    // 8KB head + 8KB tail
	readBufSize = 64 * 1024 // 64KB read buffer for full hashing
)

// ComputePartialHash reads the first 8KB and last 8KB of a file and returns
// the xxHash64 of their concatenation. For files <= 8KB, returns the hash of
// the entire content and sets isFull=true.
func ComputePartialHash(path string, size int64) (hash string, isFull bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer f.Close()

	h := xxhash.New()

	if size <= partialSize {
		// Small file: hash the whole thing
		if _, err := io.Copy(h, f); err != nil {
			return "", false, err
		}
		return fmt.Sprintf("%016x", h.Sum64()), true, nil
	}

	// Read first 8KB
	head := make([]byte, partialSize)
	n, err := io.ReadFull(f, head)
	if err != nil {
		return "", false, fmt.Errorf("read head: %w", err)
	}
	h.Write(head[:n])

	// Seek to last 8KB
	if _, err := f.Seek(-partialSize, io.SeekEnd); err != nil {
		return "", false, fmt.Errorf("seek tail: %w", err)
	}

	tail := make([]byte, partialSize)
	n, err = io.ReadFull(f, tail)
	if err != nil {
		return "", false, fmt.Errorf("read tail: %w", err)
	}
	h.Write(tail[:n])

	return fmt.Sprintf("%016x", h.Sum64()), false, nil
}

// ComputeFullHash streams the entire file through xxHash64 in 64KB chunks.
func ComputeFullHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := xxhash.New()
	buf := make([]byte, readBufSize)
	if _, err := io.CopyBuffer(h, f, buf); err != nil {
		return "", err
	}
	return fmt.Sprintf("%016x", h.Sum64()), nil
}

// PartialHash runs the partial hashing phase on all candidates.
// It processes files concurrently with the given concurrency level.
func PartialHash(ctx context.Context, store *db.Store, concurrency int) error {
	if concurrency <= 0 {
		concurrency = 2
	}

	batchSize := 1000
	for {
		candidates, err := store.PartialHashCandidates(batchSize)
		if err != nil {
			return fmt.Errorf("get candidates: %w", err)
		}
		if len(candidates) == 0 {
			break
		}

		results, err := processFiles(ctx, candidates, concurrency, func(f model.FileRecord) model.HashResult {
			hash, isFull, err := ComputePartialHash(f.Path, f.Size)
			return model.HashResult{
				FileID: f.ID,
				Path:   f.Path,
				Hash:   hash,
				Err:    err,
				IsFull: isFull,
			}
		})
		if err != nil {
			return err
		}

		if err := store.BatchUpdatePartialHashes(results); err != nil {
			return fmt.Errorf("batch update: %w", err)
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return nil
}

// FullHash runs the full hashing phase on all candidates.
func FullHash(ctx context.Context, store *db.Store, concurrency int, progressFn func(bytesHashed int64)) error {
	if concurrency <= 0 {
		concurrency = 2
	}

	batchSize := 1000
	for {
		candidates, err := store.FullHashCandidates(batchSize)
		if err != nil {
			return fmt.Errorf("get candidates: %w", err)
		}
		if len(candidates) == 0 {
			break
		}

		results, err := processFiles(ctx, candidates, concurrency, func(f model.FileRecord) model.HashResult {
			hash, err := ComputeFullHash(f.Path)
			if err == nil && progressFn != nil {
				progressFn(f.Size)
			}
			return model.HashResult{
				FileID: f.ID,
				Path:   f.Path,
				Hash:   hash,
				Err:    err,
			}
		})
		if err != nil {
			return err
		}

		if err := store.BatchUpdateFullHashes(results); err != nil {
			return fmt.Errorf("batch update: %w", err)
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return nil
}

// processFiles fans work out to concurrent workers and collects results.
func processFiles(ctx context.Context, files []model.FileRecord, concurrency int, fn func(model.FileRecord) model.HashResult) ([]model.HashResult, error) {
	work := make(chan model.FileRecord, concurrency)
	resultCh := make(chan model.HashResult, concurrency)

	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range work {
				resultCh <- fn(f)
			}
		}()
	}

	// Collect results in a goroutine
	var results []model.HashResult
	var collectErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		for r := range resultCh {
			if r.Err != nil {
				slog.Warn("Hash error", "path", r.Path, "err", r.Err)
			}
			results = append(results, r)
		}
	}()

	// Send work
	for _, f := range files {
		select {
		case <-ctx.Done():
			close(work)
			wg.Wait()
			close(resultCh)
			<-done
			return results, ctx.Err()
		case work <- f:
		}
	}
	close(work)
	wg.Wait()
	close(resultCh)
	<-done

	_ = collectErr
	return results, nil
}

// ProgressTracker tracks hashing progress for display.
type ProgressTracker struct {
	mu           sync.Mutex
	bytesHashed  int64
	totalBytes   int64
	filesHashed  int64
	totalFiles   int64
	startTime    time.Time
	lastReport   time.Time
}

// NewProgressTracker creates a new progress tracker.
func NewProgressTracker(totalFiles, totalBytes int64) *ProgressTracker {
	return &ProgressTracker{
		totalFiles: totalFiles,
		totalBytes: totalBytes,
		startTime:  time.Now(),
		lastReport: time.Now(),
	}
}

// Add records progress and logs periodically.
func (p *ProgressTracker) Add(bytes int64) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.bytesHashed += bytes
	p.filesHashed++

	if time.Since(p.lastReport) >= 10*time.Second {
		elapsed := time.Since(p.startTime).Seconds()
		speed := float64(p.bytesHashed) / elapsed / 1024 / 1024 // MB/s

		pct := float64(0)
		if p.totalBytes > 0 {
			pct = float64(p.bytesHashed) / float64(p.totalBytes) * 100
		}

		var eta string
		if speed > 0 && p.totalBytes > 0 {
			remaining := float64(p.totalBytes-p.bytesHashed) / (speed * 1024 * 1024)
			eta = (time.Duration(remaining) * time.Second).Truncate(time.Second).String()
		}

		slog.Info("Hash progress",
			"files", fmt.Sprintf("%d/%d", p.filesHashed, p.totalFiles),
			"pct", fmt.Sprintf("%.1f%%", pct),
			"speed", fmt.Sprintf("%.1f MB/s", speed),
			"eta", eta,
		)
		p.lastReport = time.Now()
	}
}
