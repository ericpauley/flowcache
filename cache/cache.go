/*
Package cache implements a cache which collaborates on concurrent calculations of the same hash key to reduce time spent calculating expensive values.

The Cache.Get() method ensures that at most one goroutine is calculating a specific key value at any time.
It also ensures that frequently accessed values are recalculated before their expiration time.
It does this by creating a cacheItem which contains a wait condition on the value completion only if one does not already exist.
*/
package cache

import (
	"errors"
	"sync"
	"time"

	"github.com/ericpauley/go-utils/memory"
)

type cacheItem struct {
	lastUsed time.Time
	created  time.Time
	ttl      time.Duration
	val      interface{}
	err      error
	future   <-chan bool
	refresh  <-chan bool
	cache    *Cache
	size     uint64
}

func (item *cacheItem) expired() bool {
	used := item.created
	if !item.lastUsed.IsZero() && item.cache.ExtendOnUse {
		used = item.lastUsed
	}
	return !used.IsZero() && item.ttl != 0 && used.Add(item.ttl).Before(time.Now())
}

func (item *cacheItem) shouldRefresh() bool {
	return item.cache.Refresh && !item.created.IsZero() && item.ttl != 0 && item.created.Add(item.ttl/2).Before(time.Now())
}

func (item *cacheItem) updateSize(key interface{}) {
	item.cache.storage -= item.size
	if item.val != nil {
		func() {
			defer func() {
				recover()
			}()
			item.size = memory.Sizeof(item.val)
		}()
	} else {
		item.size = 0
	}
	item.cache.storage += item.size
}

// Cache implements a cache
type Cache struct {
	data        map[interface{}]*cacheItem
	MaxSize     int
	MaxStorage  uint64
	mutex       sync.Mutex
	Refresh     bool
	ExtendOnUse bool
	GetTimeout  time.Duration
	Recover     bool
	storage     uint64
}

func (c *Cache) prune() {
	for len(c.data) >= c.MaxSize || (c.MaxStorage != 0 && c.storage > c.MaxStorage) {
		checked := 0
		var candidateKey interface{}
		for k, v := range c.data {
			if v.ttl == 0 || v.expired() { // Expired keys are immediate candidates for removal
				candidateKey = k
				break
			} else if candidateKey == nil || v.lastUsed.Before(c.data[candidateKey].lastUsed) {
				candidateKey = k
			}
			checked++
			if checked >= 5 {
				break
			}
		}
		c.remove(candidateKey)
	}
}

func (c *Cache) remove(candidateKey interface{}) {
	c.storage -= c.data[candidateKey].size
	delete(c.data, candidateKey)
}

func (c *Cache) generateItem(key interface{}, item *cacheItem, generate func(interface{}) (interface{}, error), future chan bool) {
	var val interface{}
	var err error
	func() {
		if c.Recover {
			defer func() {
				if r := recover(); r != nil {
					val = nil
					err = errors.New("Unknown Error")
				}
			}()
		}
		val, err = generate(key)

	}()
	c.lockMap()
	defer c.mutex.Unlock()
	if err == nil || item.refresh == nil { // Only propogate errors if this isn't a refresh
		item.val, item.err = val, err
		item.updateSize(key)
	}
	if item.refresh == nil && (item.err != nil || item.ttl == 0) {
		if c.data[key] == item {
			c.remove(key) // Don't allow anything else to use this error/instant result
		}
	}
	item.created = time.Now()
	item.refresh = nil // Clear out a refresh channel if there is one
	close(future)
}

func (c *Cache) lockMap() {
	c.mutex.Lock()
	if c.data == nil {
		c.data = make(map[interface{}]*cacheItem)
	}
}

// Get begins the process of fetching a specific cache item.
//
// When Get is called a new goroutine is spawned to run the provided generate method if no result is available in the cache.
// A function is return which, when called, will wait for the goroutine to finish execution and return the value/error returned.
// In the case that the item has expired before the retrieve method is called the goroutine may be called again.
//
// If the cache entry is half way through its TTL a new goroutine will be spawned at the time of retrieval,
// though the current cached values will be returned immediately.
//
// Expiration/Refresh conditions are evaluated immediately upon calling Get(),
// the retrieval function returns the cache query as it was evaluated during the Get operation.
func (c *Cache) Get(key interface{}, ttl time.Duration, generate func(interface{}) (interface{}, error)) func() (interface{}, error) {
	c.lockMap()
	item, ok := c.data[key]
	if !ok {
		future := make(chan bool)
		item = &cacheItem{val: nil, future: future, ttl: ttl, cache: c}
		c.prune()
		c.data[key] = item
		go c.generateItem(key, item, generate, future)
	}
	future := item.future
	c.mutex.Unlock()
	var result interface{}
	var resErr error
	resultWait := make(chan bool)
	go func() {
		<-future
		c.lockMap()
		if item.expired() {
			if item.future != nil { // The item hasn't already been destroyed
				item.future, item.refresh = item.refresh, nil // Atempt to promote the refresh routine to main provider
				if item.future == nil {                       // There is no valid refresh routine
					if item == c.data[key] {
						c.remove(key)
					}
				}
			}
			c.mutex.Unlock()
			result, resErr = c.Get(key, ttl, generate)()
			close(resultWait)
			return
		}
		defer c.mutex.Unlock()
		if item.shouldRefresh() && item.refresh == nil {
			refresh := make(chan bool)
			item.refresh = refresh
			go c.generateItem(key, item, generate, refresh)
		}
		item.ttl = ttl
		item.lastUsed = time.Now()
		result, resErr = item.val, item.err
		close(resultWait)
	}()
	c.PurgeCount(5)
	return func() (interface{}, error) {
		if c.GetTimeout != 0 {
			select {
			case <-resultWait:
				return result, resErr
			case <-time.After(c.GetTimeout):
				return nil, errors.New("Generation timed out")
			}
		}
		<-resultWait
		return result, resErr
	}
}

// Purge finds and removes all expired cache entires from the cache, allowing the data to be freed by the garbage collector.
func (c *Cache) Purge() {
	c.lockMap()
	defer c.mutex.Unlock()
	for key, val := range c.data {
		if val.expired() {
			c.remove(key)
		}
	}
}

// PurgeCount finds and removes all expired cache entires from the cache, checking at moust count items.
func (c *Cache) PurgeCount(count int) {
	processed := 0
	c.lockMap()
	defer c.mutex.Unlock()
	for key, val := range c.data {
		if val.expired() {
			c.remove(key)
		}
		processed++
		if processed >= count {
			break
		}
	}
}

// Clear removes all items from the cache.
func (c *Cache) Clear() {
	c.lockMap()
	defer c.mutex.Unlock()
	c.data = nil
}

// Size returns the number of cache entires (including unpurged expired entries) in the cache.
func (c *Cache) Size() int {
	return len(c.data)
}
