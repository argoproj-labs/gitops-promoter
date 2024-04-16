package utils

import (
	"maps"
	"sync"
)

var mutex sync.RWMutex

type PathLookup struct {
	storage map[string]string
}

func NewPathLookup() PathLookup {
	return PathLookup{
		storage: make(map[string]string),
	}
}

func (pl *PathLookup) Get(key string) string {
	mutex.RLock()
	defer mutex.RUnlock()
	return pl.storage[key]
}

func (pl *PathLookup) GetAll() map[string]string {
	mutex.RLock()
	defer mutex.RUnlock()
	return maps.Clone(pl.storage)
}

func (pl *PathLookup) Set(key string, value string) {
	mutex.Lock()
	defer mutex.Unlock()
	if pl.storage == nil {
		pl.storage = make(map[string]string)
	}
	pl.storage[key] = value
}
