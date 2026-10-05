package jev

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/tools4imps/qualm/internal/atomicfile"
)

// Cache stores settled replies on disk, one <key>.json file each. An empty Dir means the cache is
// unusable: Get always misses and Put fails.
type Cache struct{ Dir string }

// Get returns the reply stored under key. A missing, unreadable or corrupt entry is a miss, so a
// damaged cache costs a request and never fails a run.
func (c Cache) Get(key string) (Reply, bool) {
	if c.Dir == "" {
		return Reply{}, false
	}
	data, err := os.ReadFile(filepath.Join(c.Dir, key+".json"))
	if err != nil {
		return Reply{}, false
	}
	var r Reply
	if err := json.Unmarshal(data, &r); err != nil || r.Answers == nil {
		return Reply{}, false
	}
	return r, true
}

// Put stores r under key. The write is atomic, so a reader never sees half an entry and a crash
// leaves no corrupt one.
func (c Cache) Put(key string, r Reply) error {
	if c.Dir == "" {
		return errors.New("cache: no directory set")
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(c.Dir, 0o755); err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(c.Dir, key+".json"), data, 0o600)
}
