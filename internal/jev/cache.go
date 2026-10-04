package jev

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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

// Put stores r under key. The write goes to a temporary file in the same directory and is renamed
// into place, so a reader never sees half an entry and a crash leaves no corrupt one.
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
	tmp, err := os.CreateTemp(c.Dir, key+".*.tmp")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr == nil {
		werr = cerr
	}
	if werr != nil {
		os.Remove(tmp.Name())
		return werr
	}
	if err := os.Rename(tmp.Name(), filepath.Join(c.Dir, key+".json")); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}
