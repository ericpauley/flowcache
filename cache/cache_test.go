package cache

import (
	"errors"
	"testing"
	"time"
)

func getGeneratorStub(output interface{}, err error) func(string) (interface{}, error) {
	return func(string) (interface{}, error) {
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

// TestCacheStorage tests that the cache actually stores a value for a key. It gets an item then gets it again using a different generator.
func TestCacheStorage(t *testing.T) {
	c := NewCache(1)
	ttl := 100 * time.Second
	expectCacheValue(t, c, "test", ttl, "A", "A", "Key generator did not get called correctly.")
	expectCacheValue(t, c, "test", ttl, "B", "A", "Cache did not persist item. ")
}

// TestLRUPrune ensures that the cache prunes the least recently used item.
func TestLRUPrune(t *testing.T) {
	c := NewCache(3)
	ttl := 100 * time.Second
	setCacheValue(t, c, "A", ttl, "A")
	setCacheValue(t, c, "B", ttl, "B")
	setCacheValue(t, c, "C", ttl, "C")
	setCacheValue(t, c, "D", ttl, "D")
	expectCacheValue(t, c, "B", ttl, "test", "B", "Cache item B was incorrectly evicted.")
	expectCacheValue(t, c, "A", ttl, "test", "test", "Cache item A was not correctly evicted.")
}

func TestExpirePrune(t *testing.T) {
	c := NewCache(3)
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
	c := NewCache(5)
	for _, key := range "ABCDEF" {
		expectCacheValue(t, c, string(key), 100*time.Second, string(key), string(key), "Key generator did not get called correctly.")
	}
}

func TestRefresh(t *testing.T) {
	c := NewCache(1)
	future := make(chan bool)
	close(future)
	c.data["test"] = &cacheItem{future: future, ttl: 100 * time.Second, created: time.Now().Add(-75 * time.Second), val: "A"}
	expectCacheValue(t, c, "test", 100*time.Second, "B", "A", "Cache item was not present")
	time.Sleep(1 * time.Millisecond)
	expectCacheValue(t, c, "test", 100*time.Second, "C", "B", "Cache item was not updated")
}

func TestErrorPropogation(t *testing.T) {
	c := NewCache(1)
	_, err := c.Get("test", 100*time.Second, func(string) (interface{}, error) {
		return nil, errors.New("Test Error")
	})()
	if err == nil {
		t.Fatal("Cache did not return generation error.")
	}
	expectCacheValue(t, c, "test", 100*time.Second, "A", "A", "Key generator did not get called correctly.")
}

func TestExpiredRefetch(t *testing.T) {
	c := NewCache(1)
	future := make(chan bool)
	close(future)
	c.data["test"] = &cacheItem{future: future, ttl: 10 * time.Second, created: time.Now().Add(-75 * time.Second), val: "A"}
	expectCacheValue(t, c, "test", 100*time.Second, "B", "B", "Old key did not get expired correctly")
}
