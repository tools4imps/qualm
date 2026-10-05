package check

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/tools4imps/qualm/internal/config"
	"github.com/tools4imps/qualm/internal/gitdiff"
)

// Keep records a keep for each path and returns every keep now in qualm.json.
func Keep(o Options, reason string) ([]config.Keep, error) {
	// The reason is what a reviewer of qualm.json reads, so a keep without one says nothing.
	if strings.TrimSpace(reason) == "" {
		return nil, errors.New("a keep needs a reason")
	}
	if len(o.Paths) == 0 {
		return nil, errors.New("a keep needs at least one path")
	}
	cfg, err := config.Load(o.Dir)
	if err != nil {
		return nil, err
	}
	// Every change is listed, not only the paths given, so that keeps gone stale anywhere in the
	// repository are dropped.
	base, changes, err := changesSince(o.Dir, o.Base, nil)
	if err != nil {
		return nil, err
	}
	hashOf := func(path string) (string, error) { return hashAt(o.Dir, base, changes, path) }
	keeps, err := live(cfg.Keeps, hashOf)
	if err == nil {
		err = addKeeps(keeps, o.Paths, reason, today(o.Now), hashOf)
	}
	if err != nil {
		return nil, err
	}
	cfg.Keeps = slices.SortedFunc(maps.Values(keeps), func(a, b config.Keep) int { return cmp.Compare(a.Path, b.Path) })
	if err := cfg.Save(o.Dir); err != nil {
		return nil, err
	}
	return cfg.Keeps, nil
}

// live returns the keeps that still bind their file's change, by path. The rest have gone stale.
func live(keeps []config.Keep, hashOf func(path string) (string, error)) (map[string]config.Keep, error) {
	out := map[string]config.Keep{}
	for _, k := range keeps {
		hash, err := hashOf(k.Path)
		if err != nil {
			return nil, err
		}
		if hash != "" && hash == k.Change {
			out[k.Path] = k
		}
	}
	return out, nil
}

// addKeeps puts a keep for each path's current change into keeps, over any keep the path had.
func addKeeps(keeps map[string]config.Keep, paths []string, reason, date string, hashOf func(path string) (string, error)) error {
	for _, p := range paths {
		// Git names paths with slashes and no leading "./", however the path was typed.
		path := filepath.ToSlash(filepath.Clean(p))
		hash, err := hashOf(path)
		if err != nil {
			return err
		}
		if hash == "" {
			return fmt.Errorf("%s has no change against the base to keep", p)
		}
		keeps[path] = config.Keep{Path: path, Change: hash, Reason: reason, Date: date}
	}
	return nil
}

// hashAt reads the change at path and returns its hash. The hash is "" when the path has no
// change, or a change with no lines to keep. Only the paths a keep names are read.
func hashAt(dir, base string, changes []gitdiff.Change, path string) (string, error) {
	i := slices.IndexFunc(changes, func(c gitdiff.Change) bool { return c.Path == path })
	if i < 0 {
		return "", nil
	}
	d, err := gitdiff.Read(dir, base, changes[i])
	if err != nil || d.Text == "" {
		return "", err
	}
	return changeHash(d.Text), nil
}

// today is the date a keep made now carries.
func today(now func() time.Time) string {
	if now == nil {
		now = time.Now
	}
	return now().Format("2006-01-02")
}

// changeHash is what a keep binds to: the SHA-256, in hex, of the lines a normalised diff adds and
// removes, in order. Any further edit to those lines makes a different hash and the file is judged
// again. The line numbers and the lines around the change are left out, so that a change somewhere
// else in the file on the base branch leaves the keep standing.
func changeHash(diff string) string {
	h := sha256.New()
	_, hunks := gitdiff.Hunks(diff)
	for _, hunk := range hunks {
		for _, line := range strings.SplitAfter(hunk.Text, "\n") {
			if strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") {
				io.WriteString(h, line)
			}
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// stale lists the keeps that bind no change, in the order they were given. A keep for a file the
// run skipped is left alone: nothing was read to compare it with.
func stale(keeps []config.Keep, files []File, diffs []string) []config.Keep {
	var out []config.Keep
	for _, k := range keeps {
		i := slices.IndexFunc(files, func(f File) bool { return f.Path == k.Path })
		if i < 0 || files[i].Status != StatusSkipped && k.Change != changeHash(diffs[i]) {
			out = append(out, k)
		}
	}
	return out
}
