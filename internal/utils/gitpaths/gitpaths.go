package gitpaths

import (
	"sync"
)

var storage sync.Map

// Get retrieves the path associated with the given key from the storage.
func Get(key string) string {
	path, ok := storage.Load(key)
	if !ok {
		return ""
	}
	//nolint:forcetypeassert
	return path.(string)
}

// GetValues returns all paths stored in the storage.
func GetValues() []string {
	var values []string
	storage.Range(func(key, path any) bool {
		//nolint:forcetypeassert
		values = append(values, path.(string))
		return true
	})
	return values
}

// Set stores a path for the given key.
func Set(key string, path string) {
	storage.Store(key, path)
}
