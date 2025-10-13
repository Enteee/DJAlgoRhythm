package store

import (
	"fmt"
	"testing"
)

func TestDedupStore_Basic(t *testing.T) {
	store := NewDedupStore(100, 0.001)

	// Test empty store
	if store.Has("track1") {
		t.Error("Empty store should not have any tracks")
	}

	if store.Size() != 0 {
		t.Errorf("Empty store size should be 0, got %d", store.Size())
	}

	// Test adding tracks
	store.Add("track1")
	if !store.Has("track1") {
		t.Error("Store should have track1 after adding")
	}

	if store.Size() != 1 {
		t.Errorf("Store size should be 1 after adding one track, got %d", store.Size())
	}

	// Test duplicate addition
	store.Add("track1")
	if store.Size() != 1 {
		t.Errorf("Store size should still be 1 after adding duplicate, got %d", store.Size())
	}

	// Test multiple tracks
	store.Add("track2")
	store.Add("track3")

	if store.Size() != 3 {
		t.Errorf("Store size should be 3 after adding three tracks, got %d", store.Size())
	}

	if !store.Has("track2") || !store.Has("track3") {
		t.Error("Store should have all added tracks")
	}
}

func TestDedupStore_Load(t *testing.T) {
	store := NewDedupStore(100, 0.001)

	// Load initial tracks
	tracks := []string{"track1", "track2", "track3"}
	store.Load(tracks)

	if store.Size() != 3 {
		t.Errorf("Store size should be 3 after loading, got %d", store.Size())
	}

	for _, track := range tracks {
		if !store.Has(track) {
			t.Errorf("Store should have loaded track %s", track)
		}
	}

	// Load again with different tracks
	newTracks := []string{"track4", "track5"}
	store.Load(newTracks)

	if store.Size() != 2 {
		t.Errorf("Store size should be 2 after reloading, got %d", store.Size())
	}

	// Old tracks should be gone
	for _, track := range tracks {
		if store.Has(track) {
			t.Errorf("Store should not have old track %s after reload", track)
		}
	}

	// New tracks should be present
	for _, track := range newTracks {
		if !store.Has(track) {
			t.Errorf("Store should have new track %s", track)
		}
	}
}

func TestDedupStore_LoadWithEmptyStrings(t *testing.T) {
	store := NewDedupStore(100, 0.001)

	// Load tracks with empty strings
	tracks := []string{"track1", "", "track2", "", "track3"}
	store.Load(tracks)

	// Should only have non-empty tracks
	if store.Size() != 3 {
		t.Errorf("Store size should be 3 after loading (ignoring empty strings), got %d", store.Size())
	}

	expectedTracks := []string{"track1", "track2", "track3"}
	for _, track := range expectedTracks {
		if !store.Has(track) {
			t.Errorf("Store should have track %s", track)
		}
	}
}

func TestDedupStore_Clear(t *testing.T) {
	store := NewDedupStore(100, 0.001)

	// Add some tracks
	tracks := []string{"track1", "track2", "track3"}
	for _, track := range tracks {
		store.Add(track)
	}

	if store.Size() != 3 {
		t.Errorf("Store size should be 3 before clear, got %d", store.Size())
	}

	// Clear the store
	store.Clear()

	if store.Size() != 0 {
		t.Errorf("Store size should be 0 after clear, got %d", store.Size())
	}

	for _, track := range tracks {
		if store.Has(track) {
			t.Errorf("Store should not have track %s after clear", track)
		}
	}
}

func TestDedupStore_MaxCapacity(t *testing.T) {
	maxTracks := 5
	store := NewDedupStore(maxTracks, 0.001)

	// Add more tracks than the maximum
	for i := 0; i < maxTracks+3; i++ {
		trackID := fmt.Sprintf("track%d", i)
		store.Add(trackID)
	}

	// Store should not exceed maximum capacity
	if store.Size() > maxTracks {
		t.Errorf("Store size should not exceed %d, got %d", maxTracks, store.Size())
	}

	// The most recently added tracks should be present
	recentTracks := []string{"track5", "track6", "track7"}
	for _, track := range recentTracks {
		if !store.Has(track) {
			t.Errorf("Store should have recent track %s", track)
		}
	}
}

func TestDedupStore_BloomFilterEffectiveness(t *testing.T) {
	store := NewDedupStore(1000, 0.001)

	// Add a large number of tracks
	numTracks := 500
	for i := 0; i < numTracks; i++ {
		trackID := fmt.Sprintf("track_%d", i)
		store.Add(trackID)
	}

	// All added tracks should be found
	for i := 0; i < numTracks; i++ {
		trackID := fmt.Sprintf("track_%d", i)
		if !store.Has(trackID) {
			t.Errorf("Store should have track %s", trackID)
		}
	}

	// Non-existent tracks should not be found (with high probability)
	falsePositives := 0
	testCount := 1000

	for i := numTracks; i < numTracks+testCount; i++ {
		trackID := fmt.Sprintf("nonexistent_%d", i)
		if store.Has(trackID) {
			falsePositives++
		}
	}

	// False positive rate should be very low (well below 1%)
	falsePositiveRate := float64(falsePositives) / float64(testCount)
	if falsePositiveRate > 0.01 {
		t.Errorf("Bloom filter false positive rate too high: %f (expected < 0.01)", falsePositiveRate)
	}
}

func BenchmarkDedupStore_Add(b *testing.B) {
	store := NewDedupStore(10000, 0.001)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		trackID := fmt.Sprintf("track_%d", i)
		store.Add(trackID)
	}
}

func BenchmarkDedupStore_Has(b *testing.B) {
	store := NewDedupStore(10000, 0.001)

	// Pre-populate with some tracks
	for i := 0; i < 1000; i++ {
		trackID := fmt.Sprintf("track_%d", i)
		store.Add(trackID)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		trackID := fmt.Sprintf("track_%d", i%1000)
		store.Has(trackID)
	}
}

func BenchmarkDedupStore_Load(b *testing.B) {
	store := NewDedupStore(10000, 0.001)

	// Prepare track list
	tracks := make([]string, 1000)
	for i := 0; i < 1000; i++ {
		tracks[i] = fmt.Sprintf("track_%d", i)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store.Load(tracks)
	}
}

