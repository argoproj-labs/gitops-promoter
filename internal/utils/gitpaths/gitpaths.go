package gitpaths

import (
	"sync"
)

var storage sync.Map

func Get(key string) string {
	v, ok := storage.Load(key)
	if !ok {
		return ""
	}
	//nolint:forcetypeassert
	return v.(string)
}

func GetValues() []string {
	var values []string
	storage.Range(func(key, value any) bool {
		//nolint:forcetypeassert
		values = append(values, value.(string))
		return true
	})
	return values
}

func Set(key string, value string) {
	storage.Store(key, value)
}
