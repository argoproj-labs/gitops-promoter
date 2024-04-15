package utils

import "sync"

var mutex sync.RWMutex

type PathLookup struct {
	storage map[string]string
}

func NewPathLookup() PathLookup {
	return PathLookup{}
}

func (pl *PathLookup) Get(key string) string {
	mutex.RLock()
	defer mutex.RUnlock()
	return pl.storage[key]
}

func (pl *PathLookup) Set(key string, value string) {
	mutex.Lock()
	defer mutex.Unlock()
	pl.storage[key] = value
}
