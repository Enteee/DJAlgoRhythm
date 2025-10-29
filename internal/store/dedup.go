// Package store provides deduplication storage using Bloom filters and LRU cache.
package store

import (
	"sync"

	"github.com/bits-and-blooms/bloom/v3"
	lru "github.com/hashicorp/golang-lru/v2"
)

// DedupStore provides thread-safe deduplication storage using Bloom filters and LRU cache.
type DedupStore struct {
	trackIDs               map[string]struct{}
	bloom                  *bloom.BloomFilter
	lru                    *lru.Cache[string, struct{}]
	mutex                  sync.RWMutex
	maxTracks              int
	bloomFalsePositiveRate float64
}

// NewDedupStore creates a new deduplication store with the specified capacity and false positive rate.
func NewDedupStore(maxTracks int, bloomFalsePositiveRate float64) *DedupStore {
	lruCache, _ := lru.New[string, struct{}](maxTracks)

	if maxTracks < 0 || maxTracks > int(^uint(0)>>1) {
		panic("maxTracks value out of range for uint conversion")
	}
	bloomFilter := bloom.NewWithEstimates(uint(maxTracks), bloomFalsePositiveRate)

	return &DedupStore{
		trackIDs:               make(map[string]struct{}),
		bloom:                  bloomFilter,
		lru:                    lruCache,
		maxTracks:              maxTracks,
		bloomFalsePositiveRate: bloomFalsePositiveRate,
	}
}

// Has checks if a track ID exists in the deduplication store.
func (ds *DedupStore) Has(trackID string) bool {
	ds.mutex.RLock()
	defer ds.mutex.RUnlock()

	if !ds.bloom.TestString(trackID) {
		return false
	}

	_, exists := ds.trackIDs[trackID]
	return exists
}

// Add adds a track ID to the deduplication store.
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

// Remove removes a track ID from the deduplication store.
func (ds *DedupStore) Remove(trackID string) {
	ds.mutex.Lock()
	defer ds.mutex.Unlock()

	if _, exists := ds.trackIDs[trackID]; !exists {
		return // Track not in store, nothing to remove
	}

	delete(ds.trackIDs, trackID)
	ds.lru.Remove(trackID)
	// Note: We can't remove from bloom filter as it doesn't support removal
	// This may cause false positives, but that's acceptable for this use case
}

// Load clears the store and loads the provided track IDs.
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

// Size returns the number of track IDs currently stored.
func (ds *DedupStore) Size() int {
	ds.mutex.RLock()
	defer ds.mutex.RUnlock()
	return len(ds.trackIDs)
}

// Clear removes all track IDs from the store.
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
