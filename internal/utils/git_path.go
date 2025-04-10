package utils

import (
	"sync"
)

type PathLookup struct {
	storage sync.Map
}

func NewPathLookup() PathLookup {
	return PathLookup{
		storage: sync.Map{},
	}
}

func (pl *PathLookup) Get(key string) string {
	v, ok := pl.storage.Load(key)
	if !ok {
		return ""
	}
	return v.(string)
}

func (pl *PathLookup) GetAll() map[string]string {
	result := make(map[string]string)
	pl.storage.Range(func(key, value interface{}) bool {
		result[key.(string)] = value.(string)
		return true
	})
	return result
}

func (pl *PathLookup) Set(key string, value string) {
	pl.storage.Store(key, value)
}
