package gitpaths

import (
	"sync"
)

// Key uniquely identifies an on-disk clone. It is a struct rather than a concatenated string so that
// distinct field combinations can never collide (e.g. {RepoURL: "ab", ActiveBranch: "c"} vs
// {RepoURL: "a", ActiveBranch: "bc"} would both yield "abc" as a string key).
type Key struct {
	// RepoURL is the HTTPS URL of the repository.
	RepoURL string
	// ActiveBranch is the environment's active branch.
	ActiveBranch string
	// Identity is an opaque, caller-supplied identifier that scopes the clone to a single logical owner, so that
	// distinct identities map to distinct clones.
	Identity string
}

var storage sync.Map

// Get retrieves the path associated with the given key from the storage.
func Get(key Key) string {
	path, ok := storage.Load(key)
	if !ok {
		return ""
	}
	//nolint:forcetypeassert // sync.Map stores string values, type is guaranteed
	return path.(string)
}

// GetValues returns all paths stored in the storage.
func GetValues() []string {
	var values []string
	storage.Range(func(key, path any) bool {
		//nolint:forcetypeassert // sync.Map stores string values, type is guaranteed
		values = append(values, path.(string))
		return true
	})
	return values
}

// Set stores a path for the given key.
func Set(key Key, path string) {
	storage.Store(key, path)
}
