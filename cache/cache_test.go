package cache

import (
	"errors"
	"math/rand"
	"sync"
	"testing"
	"time"
)

func getGeneratorStub(output interface{}, err error) func(interface{}) (interface{}, error) {
	return func(interface{}) (interface{}, error) {
		return output, err
	}
}

func noError(t *testing.T, err error) {
	if err != nil {
		t.Fatal(err)
	}
}

func expectCacheValue(t *testing.T, c *Cache, key string, ttl time.Duration, val string, expected string, message string) string {
	actual, err := c.Get(key, ttl, getGeneratorStub(val, nil))()
	noError(t, err)
	if actual != expected {
		t.Fatalf("%s (%s != %s)", message, actual, expected)
	}
	return expected
}

func setCacheValue(t *testing.T, c *Cache, key string, ttl time.Duration, val string) {
	_, err := c.Get(key, ttl, getGeneratorStub(val, nil))()
	noError(t, err)
}

func TestIntegrateCache(t *testing.T) {
	c := &Cache{MaxSize: 128}
	w := sync.WaitGroup{}
	for i := 0; i < 10; i++ {
		w.Add(1)
		go func() {
			for i := 0; i < 10000; i++ {
				key := rand.Uint32() % 256
				c.Get(key, time.Duration(rand.Uint32()%200)*time.Millisecond, func(arg3 interface{}) (interface{}, error) {
					time.Sleep(time.Duration(rand.Uint32()%1000) * time.Microsecond)
					return 0, nil
				})
			}
			w.Done()
		}()
	}
	w.Wait()
}

// TestCacheStorage tests that the cache actually stores a value for a key. It gets an item then gets it again using a different generator.
func TestCacheStorage(t *testing.T) {
	c := &Cache{MaxSize: 1}
	ttl := 100 * time.Second
	expectCacheValue(t, c, "test", ttl, "A", "A", "Key generator did not get called correctly.")
	expectCacheValue(t, c, "test", ttl, "B", "A", "Cache did not persist item. ")
	if c.Size() != 1 {
		t.Fatal("Cache size not properly reported.")
	}
}

// TestLRUPrune ensures that the cache prunes the least recently used item.
func TestLRUPrune(t *testing.T) {
	c := &Cache{MaxSize: 3}
	ttl := 100 * time.Second
	setCacheValue(t, c, "A", ttl, "A")
	setCacheValue(t, c, "B", ttl, "B")
	setCacheValue(t, c, "C", ttl, "C")
	setCacheValue(t, c, "D", ttl, "D")
	expectCacheValue(t, c, "B", ttl, "test", "B", "Cache item B was incorrectly evicted.")
	expectCacheValue(t, c, "A", ttl, "test", "test", "Cache item A was not correctly evicted.")
}

// TestLRUPrune ensures that the cache prunes the least recently used item.
func TestClear(t *testing.T) {
	c := &Cache{MaxSize: 3}
	ttl := 100 * time.Second
	setCacheValue(t, c, "A", ttl, "A")
	c.Clear()
	expectCacheValue(t, c, "A", ttl, "test", "test", "Cache item A was not cleared.")
}

func TestExpirePrune(t *testing.T) {
	c := &Cache{MaxSize: 3}
	ttl := 100 * time.Second
	setCacheValue(t, c, "A", ttl, "A")
	setCacheValue(t, c, "B", ttl, "B")
	expectCacheValue(t, c, "C", 1*time.Millisecond, "C", "C", "Cache item C was expired too quickly")
	time.Sleep(1 * time.Millisecond)
	setCacheValue(t, c, "D", ttl, "D")
	if _, ok := c.data["C"]; ok {
		t.Fatal("Cache item C was not correctly evicted.")
	}
}

func TestPruneLimit(t *testing.T) {
	c := &Cache{MaxSize: 5}
	for _, key := range "ABCDEF" {
		expectCacheValue(t, c, string(key), 100*time.Second, string(key), string(key), "Key generator did not get called correctly.")
	}
}

func TestRefresh(t *testing.T) {
	c := &Cache{MaxSize: 1, Refresh: true}
	c.Purge()
	future := make(chan bool)
	close(future)
	c.data["test"] = &cacheItem{cache: c, future: future, ttl: 100 * time.Second, created: time.Now().Add(-75 * time.Second), val: "A"}
	expectCacheValue(t, c, "test", 100*time.Second, "B", "A", "Cache item was not present")
	time.Sleep(1 * time.Millisecond)
	expectCacheValue(t, c, "test", 100*time.Second, "C", "B", "Cache item was not updated")
}

func TestErrorPropogation(t *testing.T) {
	c := &Cache{MaxSize: 1}
	_, err := c.Get("test", 100*time.Second, func(interface{}) (interface{}, error) {
		return nil, errors.New("Test Error")
	})()
	if err == nil {
		t.Fatal("Cache did not return generation error.")
	}
	expectCacheValue(t, c, "test", 100*time.Second, "A", "A", "Key generator did not get called correctly.")
}

func TestExpiredRefetch(t *testing.T) {
	c := &Cache{MaxSize: 1}
	c.Purge()
	future := make(chan bool)
	close(future)
	c.data["test"] = &cacheItem{cache: c, future: future, ttl: 10 * time.Second, created: time.Now().Add(-75 * time.Second), val: "A"}
	expectCacheValue(t, c, "test", 100*time.Second, "B", "B", "Old key did not get expired correctly")
}

func TestExtendOnUse(t *testing.T) {
	c := &Cache{MaxSize: 1, ExtendOnUse: true}
	c.Purge()
	future := make(chan bool)
	close(future)
	c.data["test"] = &cacheItem{cache: c, future: future, ttl: 10 * time.Second, created: time.Now().Add(-75 * time.Second), lastUsed: time.Now(), val: "A"}
	expectCacheValue(t, c, "test", 100*time.Second, "B", "A", "Key was expired despite being used")
}

func TestTimeout(t *testing.T) {
	c := &Cache{MaxSize: 1, GetTimeout: 10 * time.Millisecond}
	c.Purge()
	val, err := c.Get("test", 100*time.Second, func(arg3 interface{}) (interface{}, error) {
		return "A", nil
	})()
	if val != "A" || err != nil {
		t.Fatal("Cache expired valid fetch")
	}
	_, err = c.Get("test2", 100*time.Second, func(arg3 interface{}) (interface{}, error) {
		time.Sleep(1 * time.Second)
		return "A", nil
	})()
	if err == nil {
		t.Fatal("Cache did not expire bad fetch")
	}
}

func TestExpiredPurge(t *testing.T) {
	c := &Cache{MaxSize: 1}
	c.Purge()
	future := make(chan bool)
	close(future)
	c.data["test"] = &cacheItem{cache: c, future: future, ttl: 10 * time.Second, created: time.Now().Add(-75 * time.Second), val: "A"}
	if !c.data["test"].expired() {
		t.Fatal("Expired cacheItem did not properly indicate expired()")
	}
	c.Purge()
	if c.Size() != 0 {
		t.Fatal("Expired cache item was not purged.")
	}
}
