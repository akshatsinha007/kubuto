package cache

// Interface is the interface for a cache.
type Interface interface {
	Get(key string) (interface{}, bool)
	Set(key string, value interface{})
	Delete(key string)
}
