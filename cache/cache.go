package cache

import (
	"sync"
	"time"
)

// Future defines an object which can eventually deliver a value
type Future interface {
	Get() (interface{}, error)
}

type cacheItem struct {
	lastUsed time.Time
	created  time.Time
	ttl      time.Duration
	val      interface{}
	err      error
	future   <-chan bool
	refresh  <-chan bool
	mutex    sync.Mutex
}

func (item *cacheItem) expired() bool {
	return item.ttl != 0 && item.created.Add(item.ttl).Before(time.Now())
}

func (item *cacheItem) shouldRefresh() bool {
	return item.ttl != 0 && item.created.Add(item.ttl/2).Before(time.Now())
}

// Cache implements a cache
type Cache interface {
	Get(key string, ttl time.Duration, generate func(string) (interface{}, error)) func() (interface{}, error)
	Purge()
	Size() int
	Resize(new int) int
}

type cache struct {
	data    map[string]*cacheItem
	maxSize int
	mutex   sync.Mutex
}

// prune removes LRU elements from the cache until its size is newSize
func (c *cache) prune(newSize int) {
	for len(c.data) > newSize {
		checked := 0
		var candidateKey string
		for k, v := range c.data {
			if v.ttl == 0 || v.expired() { // Expired keys are immediate candidates for removal
				candidateKey = k
				break
			} else if candidateKey == "" || v.lastUsed.Before(c.data[candidateKey].lastUsed) {
				candidateKey = k
			}
			checked++
			if checked >= 5 {
				break
			}
		}
		delete(c.data, candidateKey)
	}
}

func (c *cache) generateItem(key string, item *cacheItem, generate func(string) (interface{}, error), future chan bool) {
	val, err := generate(key)
	item.mutex.Lock()
	defer item.mutex.Unlock()
	item.val, item.err = val, err
	if item.err != nil || item.ttl == 0 {
		c.mutex.Lock()
		delete(c.data, key) // Don't allow anything else to use this error/instant result
		c.mutex.Unlock()
	}
	item.created = time.Now()
	item.future = future // Force promote this channel to future
	item.refresh = nil   // Clear out a refresh channel if there is one
	close(future)
}

// Get begins the process of fetching a specific cache item.
//
// When Get is called a new goroutine is spawned to run the provided generate method if no result is available in the cache.
// A function is return which, when called, will wait for the goroutine to finish execution and return the value/error returned.
// In the case that the item has expired before the retrieve method is called the goroutine may be called again.
//
// If the cache entry is half way through its TTL a new goroutine will be spawned at the time of retrieval,
// though the current cached values will be returned immediately.
func (c *cache) Get(key string, ttl time.Duration, generate func(string) (interface{}, error)) func() (interface{}, error) {
	c.mutex.Lock()
	item, ok := c.data[key]
	if !ok {
		future := make(chan bool)
		item = &cacheItem{val: nil, future: future, ttl: ttl}
		c.prune(c.maxSize - 1)
		c.data[key] = item
		go c.generateItem(key, item, generate, future)
	}
	c.mutex.Unlock()
	return func() (interface{}, error) {
		<-item.future
		item.mutex.Lock()
		if item.expired() {
			if item.future != nil { // The item hasn't already been destroyed
				item.future, item.refresh = item.refresh, nil // Atempt to promote the refresh routine to main provider
				if item.future == nil {                       // There is no valid refresh routine
					c.mutex.Lock()
					delete(c.data, key)
					c.mutex.Unlock()
				}
			}
			item.mutex.Unlock()
			return c.Get(key, ttl, generate)()
		}
		if item.shouldRefresh() {
			refresh := make(chan bool)
			item.refresh = refresh
			go c.generateItem(key, item, generate, refresh)
		}
		item.ttl = ttl
		item.lastUsed = time.Now()
		item.mutex.Unlock()
		return item.val, item.err
	}
}

func (c *cache) Purge() {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	for key, val := range c.data {
		if val.expired() {
			delete(c.data, key)
		}
	}
}

func (c *cache) Size() int {
	return len(c.data)
}

func (c *cache) Resize(new int) (old int) {
	old = c.maxSize
	if new != 0 {
		c.maxSize = new
	}
	c.prune(new) // Immediately prune the cache to fit into the provided size.
	return
}

// NewCache creates a new cache of size maxSize
func NewCache(maxSize int) Cache {
	return &cache{data: make(map[string]*cacheItem), maxSize: maxSize}
}
