// Package store provides deduplication storage using Bloom filters and LRU cache.
package store

import (
	"sync"

	"github.com/bits-and-blooms/bloom/v3"
	lru "github.com/hashicorp/golang-lru/v2"
)

type DedupStore struct {
	trackIDs               map[string]struct{}
	bloom                  *bloom.BloomFilter
	lru                    *lru.Cache[string, struct{}]
	mutex                  sync.RWMutex
	maxTracks              int
	bloomFalsePositiveRate float64
}

func NewDedupStore(maxTracks int, bloomFalsePositiveRate float64) *DedupStore {
	lruCache, _ := lru.New[string, struct{}](maxTracks)

	if maxTracks < 0 || maxTracks > int(^uint(0)>>1) {
		panic("maxTracks value out of range for uint conversion")
	}
	bloom := bloom.NewWithEstimates(uint(maxTracks), bloomFalsePositiveRate)

	return &DedupStore{
		trackIDs:               make(map[string]struct{}),
		bloom:                  bloom,
		lru:                    lruCache,
		maxTracks:              maxTracks,
		bloomFalsePositiveRate: bloomFalsePositiveRate,
	}
}

func (ds *DedupStore) Has(trackID string) bool {
	ds.mutex.RLock()
	defer ds.mutex.RUnlock()

	if !ds.bloom.TestString(trackID) {
		return false
	}

	_, exists := ds.trackIDs[trackID]
	return exists
}

func (ds *DedupStore) Add(trackID string) {
	ds.mutex.Lock()
	defer ds.mutex.Unlock()

	if _, exists := ds.trackIDs[trackID]; exists {
		return
	}

	ds.trackIDs[trackID] = struct{}{}
	ds.bloom.AddString(trackID)
	ds.lru.Add(trackID, struct{}{})

	if len(ds.trackIDs) > ds.maxTracks {
		ds.evictOldest()
	}
}

func (ds *DedupStore) Load(trackIDs []string) {
	ds.mutex.Lock()
	defer ds.mutex.Unlock()

	ds.clear()

	for _, trackID := range trackIDs {
		if trackID != "" {
			ds.trackIDs[trackID] = struct{}{}
			ds.bloom.AddString(trackID)
			ds.lru.Add(trackID, struct{}{})
		}
	}

	for len(ds.trackIDs) > ds.maxTracks {
		ds.evictOldest()
	}
}

func (ds *DedupStore) Size() int {
	ds.mutex.RLock()
	defer ds.mutex.RUnlock()
	return len(ds.trackIDs)
}

func (ds *DedupStore) Clear() {
	ds.mutex.Lock()
	defer ds.mutex.Unlock()
	ds.clear()
}

func (ds *DedupStore) clear() {
	ds.trackIDs = make(map[string]struct{})
	if ds.maxTracks < 0 || ds.maxTracks > int(^uint(0)>>1) {
		panic("maxTracks value out of range for uint conversion")
	}
	ds.bloom = bloom.NewWithEstimates(uint(ds.maxTracks), ds.bloomFalsePositiveRate)
	ds.lru.Purge()
}

func (ds *DedupStore) evictOldest() {
	if ds.lru.Len() == 0 {
		return
	}

	oldestKey, _, ok := ds.lru.GetOldest()
	if !ok {
		return
	}

	delete(ds.trackIDs, oldestKey)
	ds.lru.Remove(oldestKey)
}
