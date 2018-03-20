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

func expectConsistentCacheSize(t *testing.T, c *Cache) {
	c.lockMap()
	defer c.mutex.Unlock()
	var s uint64
	for _, v := range c.data {
		s += v.size
	}
	if s != c.storage {
		t.Fatal("Cache storage inconsistent")
	}
}

func expectCacheValue(t *testing.T, c *Cache, key string, ttl time.Duration, val string, expected string, message string) string {
	actual, err := c.Get(key, ttl, getGeneratorStub(val, nil))()
	noError(t, err)
	if actual != expected {
		t.Fatalf("%s (%s != %s)", message, actual, expected)
	}
	expectConsistentCacheSize(t, c)
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
func TestPanic(t *testing.T) {
	c := &Cache{MaxSize: 3, Recover: true}
	c.Get("test", 1*time.Second, func(arg3 interface{}) (interface{}, error) {
		panic("Oops!")
	})
}

// TestLRUPrune ensures that the cache prunes the least recently used item.
func TestClear(t *testing.T) {
	c := &Cache{MaxSize: 3}
	ttl := 100 * time.Second
	setCacheValue(t, c, "A", ttl, "A")
	c.Clear()
	if c.storage != 0 {
		t.Fatal("Clearing cache didn't reset storage")
	}
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
	c.data["test"] = &cacheItem{future: future, ttl: 100 * time.Second, created: time.Now().Add(-75 * time.Second), val: "A"}
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
	c.data["test"] = &cacheItem{future: future, ttl: 10 * time.Second, created: time.Now().Add(-75 * time.Second), val: "A"}
	expectCacheValue(t, c, "test", 100*time.Second, "B", "B", "Old key did not get expired correctly")
}

func TestExtendOnUse(t *testing.T) {
	c := &Cache{MaxSize: 1, ExtendOnUse: true}
	c.Purge()
	future := make(chan bool)
	close(future)
	c.data["test"] = &cacheItem{future: future, ttl: 10 * time.Second, created: time.Now().Add(-75 * time.Second), lastUsed: time.Now(), val: "A"}
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
	c.data["test"] = &cacheItem{future: future, ttl: 10 * time.Second, created: time.Now().Add(-75 * time.Second), val: "A"}
	if !c.expired(c.data["test"]) {
		t.Fatal("Expired cacheItem did not properly indicate expired()")
	}
	c.Purge()
	if c.Size() != 0 {
		t.Fatal("Expired cache item was not purged.")
	}
}

func TestMaxStorage(t *testing.T) {
	c := &Cache{MaxStorage: 1000, MaxSize: 1000}

	setCacheValue(t, c, "a", 10*time.Second, "a")
	t.Logf("Storage size: %d", c.data["a"].size)
}

type S struct {
	p *S
}

func TestSelfReferentialStore(t *testing.T) {
	c := &Cache{MaxStorage: 1000, MaxSize: 1000}
	c.Purge()
	s := S{}
	s.p = &s
	val, _ := c.Get("test", 100*time.Second, func(arg3 interface{}) (interface{}, error) {
		return s, nil
	})()
	if val != s {
		t.Fatal("Self-referential value not stored")
	}
}

func TestLongCacheOps(t *testing.T) {
	c := &Cache{MaxStorage: 100000, MaxSize: 100000}
	c.Purge()
	for i := 0; i < 1000; i++ {
		c.Get(rand.Int(), 10*time.Second, func(arg3 interface{}) (interface{}, error) {
			if rand.Int31n(10) == 0 {
				return make([]byte, rand.Int31n(1000)), errors.New("Ayy")
			}
			return make([]byte, rand.Int31n(1000)), nil
		})
		expectConsistentCacheSize(t, c)
	}
}
