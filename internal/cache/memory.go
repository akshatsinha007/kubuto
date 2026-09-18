package cache

import (
	"sync"
	"time"
)

// Cache is an in-memory cache with TTL support.
type Cache struct {
	items             map[string]*cacheItem
	defaultExpiration time.Duration
	cleanupInterval   time.Duration
	mu                sync.RWMutex
}

type cacheItem struct {
	value      interface{}
	expiration time.Time
}

// New creates a new in-memory cache.
func New(defaultExpiration, cleanupInterval time.Duration) *Cache {
	c := &Cache{
		items:             make(map[string]*cacheItem),
		defaultExpiration: defaultExpiration,
		cleanupInterval:   cleanupInterval,
	}
	if cleanupInterval > 0 {
		go c.startCleanup()
	}
	return c
}

// Get retrieves an item from the cache.
func (c *Cache) Get(key string) (interface{}, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	item, found := c.items[key]
	if !found {
		return nil, false
	}

	if time.Now().After(item.expiration) {
		return nil, false
	}

	return item.value, true
}

// Set adds or updates an item in the cache.
func (c *Cache) Set(key string, value interface{}) {
	c.SetWithExpiration(key, value, c.defaultExpiration)
}

// SetWithExpiration adds or updates an item with a specific expiration.
func (c *Cache) SetWithExpiration(key string, value interface{}, duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items[key] = &cacheItem{
		value:      value,
		expiration: time.Now().Add(duration),
	}
}

// Delete removes an item from the cache.
func (c *Cache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.items, key)
}

// DeleteExpired removes all expired items from the cache.
func (c *Cache) DeleteExpired() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	for key, item := range c.items {
		if now.After(item.expiration) {
			delete(c.items, key)
		}
	}
}

// startCleanup starts a goroutine that periodically cleans up expired items.
func (c *Cache) startCleanup() {
	ticker := time.NewTicker(c.cleanupInterval)
	defer ticker.Stop()

	for range ticker.C {
		c.DeleteExpired()
	}
}

// DefaultExpiration is the default expiration time for cache items.
var DefaultExpiration = 1 * time.Hour
