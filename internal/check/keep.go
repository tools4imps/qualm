package check

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
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
	// Every change is looked at, not only the paths given, so that keeps gone stale anywhere in the
	// repository are dropped.
	_, changes, err := changesSince(o.Dir, o.Base, nil)
	if err != nil {
		return nil, err
	}
	keeps := map[string]config.Keep{}
	for _, k := range cfg.Keeps {
		if bound(k, changes) {
			keeps[k.Path] = k
		}
	}
	if err := addKeeps(keeps, changes, o.Paths, reason, today(o.Now)); err != nil {
		return nil, err
	}
	cfg.Keeps = slices.SortedFunc(maps.Values(keeps), func(a, b config.Keep) int { return cmp.Compare(a.Path, b.Path) })
	if err := cfg.Save(o.Dir); err != nil {
		return nil, err
	}
	return cfg.Keeps, nil
}

// addKeeps puts a keep for each path's current change into keeps, over any keep the path had.
func addKeeps(keeps map[string]config.Keep, changes []gitdiff.Change, paths []string, reason, date string) error {
	byPath := map[string]gitdiff.Change{}
	for _, c := range changes {
		byPath[c.Path] = c
	}
	for _, p := range paths {
		// Git names paths with slashes and no leading "./", however the path was typed.
		c, ok := byPath[filepath.ToSlash(filepath.Clean(p))]
		if !ok || c.Diff == "" {
			return fmt.Errorf("%s has no change against the base to keep", p)
		}
		keeps[c.Path] = config.Keep{Path: c.Path, Change: changeHash(c.Diff), Reason: reason, Date: date}
	}
	return nil
}

// today is the date a keep made now carries.
func today(now func() time.Time) string {
	if now == nil {
		now = time.Now
	}
	return now().Format("2006-01-02")
}

// changeHash is the SHA-256 of a normalised diff, in hex. It is what a keep binds to, so any
// further edit to the file makes a different hash and the file is judged again.
func changeHash(diff string) string {
	sum := sha256.Sum256([]byte(diff))
	return hex.EncodeToString(sum[:])
}

// binds reports whether a keep was made for exactly this change to this file.
func binds(k config.Keep, c gitdiff.Change) bool {
	return k.Path == c.Path && k.Change == changeHash(c.Diff)
}

// bound reports whether a keep binds to any of the changes. One that doesn't is stale.
func bound(k config.Keep, changes []gitdiff.Change) bool {
	return slices.ContainsFunc(changes, func(c gitdiff.Change) bool { return binds(k, c) })
}

// stale lists the keeps that bind to none of the changes, in the order they were given.
func stale(keeps []config.Keep, changes []gitdiff.Change) []config.Keep {
	var out []config.Keep
	for _, k := range keeps {
		if !bound(k, changes) {
			out = append(out, k)
		}
	}
	return out
}
