package cache

import (
	"encoding/binary"
	"errors"
	"sync"
	"time"
)

// Fuzz tests flowcache with a binary input test plan
func Fuzz(data []byte) int {
	if len(data) < 12 || len(data)%4 != 0 {
		return 0
	}
	var s sync.WaitGroup
	c := Cache{}
	c.MaxStorage, _ = binary.Uvarint(data[:8])
	data = data[8:]
	s.Add(len(data) / 4)
	for ; len(data) >= 4; data = data[4:] {
		time.Sleep(10 * time.Microsecond)
		ttl := data[0]
		key := data[1]
		val := data[2]
		err := data[3] < 64
		go func() {
			c.Get(key, time.Duration(ttl)*time.Microsecond, func(arg3 interface{}) (interface{}, error) {
				if err {
					return make([]byte, val), errors.New("Error")
				}
				return make([]byte, val), nil
			})()
			s.Done()
		}()
	}
	s.Wait()
	return 1
}
