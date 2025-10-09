package filecache_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jetify.com/pkg/filecache"
)

type testData struct {
	Value   string
	Counter int
}

func TestCacheOperations(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, cache *filecache.Cache[testData])
	}{
		{
			name: "basic set and get",
			run: func(t *testing.T, cache *filecache.Cache[testData]) {
				// Test cache miss
				_, err := cache.Get("key1")
				assert.True(t, filecache.IsCacheMiss(err))

				// Test Set and Get
				data := testData{Value: "hello", Counter: 42}
				err = cache.Set("key1", data, time.Hour)
				require.NoError(t, err)

				result, err := cache.Get("key1")
				require.NoError(t, err)
				assert.Equal(t, data, result)
			},
		},
		{
			name: "set with time",
			run: func(t *testing.T, cache *filecache.Cache[testData]) {
				data := testData{Value: "world", Counter: 123}
				expiration := time.Now().Add(time.Hour)
				err := cache.SetWithTime("key1", data, expiration)
				require.NoError(t, err)

				result, err := cache.Get("key1")
				require.NoError(t, err)
				assert.Equal(t, data, result)
			},
		},
		{
			name: "expiration",
			run: func(t *testing.T, cache *filecache.Cache[testData]) {
				data := testData{Value: "expires", Counter: 1}
				// Set with expiration in the past
				err := cache.SetWithTime("key1", data, time.Now().Add(-time.Hour))
				require.NoError(t, err)

				_, err = cache.Get("key1")
				assert.True(t, filecache.IsCacheMiss(err))
			},
		},
		{
			name: "get or set",
			run: func(t *testing.T, cache *filecache.Cache[testData]) {
				callCount := 0
				fetchFunc := func() (testData, time.Duration, error) {
					callCount++
					return testData{Value: "fetched", Counter: callCount}, time.Hour, nil
				}

				// First call should fetch
				result1, err := cache.GetOrSet("key1", fetchFunc)
				require.NoError(t, err)
				assert.Equal(t, "fetched", result1.Value)
				assert.Equal(t, 1, callCount)

				// Second call should use cache
				result2, err := cache.GetOrSet("key1", fetchFunc)
				require.NoError(t, err)
				assert.Equal(t, "fetched", result2.Value)
				assert.Equal(t, 1, callCount) // Should not increment
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := filecache.New[testData]("test-domain", filecache.WithCacheDir[testData](t.TempDir()))
			tt.run(t, cache)
		})
	}
}

func TestConcurrentAccess(t *testing.T) {
	t.Run("concurrent writes to same key", func(t *testing.T) {
		cache := filecache.New[testData]("test-domain", filecache.WithCacheDir[testData](t.TempDir()))

		numGoroutines := 10
		var wg sync.WaitGroup
		wg.Add(numGoroutines)

		// All goroutines write to the same key
		for i := 0; i < numGoroutines; i++ {
			go func(id int) {
				defer wg.Done()
				data := testData{Value: fmt.Sprintf("writer-%d", id), Counter: id}
				err := cache.Set("same-key", data, time.Hour)
				assert.NoError(t, err)
			}(i)
		}

		wg.Wait()

		// The key should exist and contain valid data from one of the writers
		result, err := cache.Get("same-key")
		require.NoError(t, err)
		assert.NotEmpty(t, result.Value)
		assert.True(t, result.Counter >= 0 && result.Counter < numGoroutines)
	})

	t.Run("concurrent writes to different keys", func(t *testing.T) {
		cache := filecache.New[testData]("test-domain", filecache.WithCacheDir[testData](t.TempDir()))

		numGoroutines := 20
		var wg sync.WaitGroup
		wg.Add(numGoroutines)

		// Each goroutine writes to a different key
		for i := 0; i < numGoroutines; i++ {
			go func(id int) {
				defer wg.Done()
				key := fmt.Sprintf("key-%d", id)
				data := testData{Value: fmt.Sprintf("value-%d", id), Counter: id}
				err := cache.Set(key, data, time.Hour)
				assert.NoError(t, err)
			}(i)
		}

		wg.Wait()

		// Verify all keys were written correctly
		for i := 0; i < numGoroutines; i++ {
			key := fmt.Sprintf("key-%d", i)
			result, err := cache.Get(key)
			require.NoError(t, err, "Failed to get key %s", key)
			assert.Equal(t, fmt.Sprintf("value-%d", i), result.Value)
			assert.Equal(t, i, result.Counter)
		}
	})

	t.Run("concurrent get or set same key", func(t *testing.T) {
		cache := filecache.New[testData]("test-domain", filecache.WithCacheDir[testData](t.TempDir()))

		numGoroutines := 10
		var wg sync.WaitGroup
		wg.Add(numGoroutines)

		callCount := 0
		var mu sync.Mutex

		fetchFunc := func() (testData, time.Duration, error) {
			mu.Lock()
			callCount++
			count := callCount
			mu.Unlock()
			// Simulate slow fetch
			time.Sleep(10 * time.Millisecond)
			return testData{Value: "shared", Counter: count}, time.Hour, nil
		}

		// All goroutines try to GetOrSet the same key
		for i := 0; i < numGoroutines; i++ {
			go func() {
				defer wg.Done()
				result, err := cache.GetOrSet("shared-key", fetchFunc)
				assert.NoError(t, err)
				assert.Equal(t, "shared", result.Value)
			}()
		}

		wg.Wait()

		// The fetch function may be called multiple times due to race,
		// but the final cached value should be valid
		result, err := cache.Get("shared-key")
		require.NoError(t, err)
		assert.Equal(t, "shared", result.Value)
		assert.True(t, result.Counter > 0)
	})

	t.Run("concurrent reads and writes", func(t *testing.T) {
		cache := filecache.New[testData]("test-domain", filecache.WithCacheDir[testData](t.TempDir()))

		// Pre-populate the cache
		err := cache.Set("key", testData{Value: "initial", Counter: 0}, time.Hour)
		require.NoError(t, err)

		numReaders := 10
		numWriters := 5
		var wg sync.WaitGroup
		wg.Add(numReaders + numWriters)

		// Spawn readers
		for i := 0; i < numReaders; i++ {
			go func() {
				defer wg.Done()
				for j := 0; j < 100; j++ {
					result, err := cache.Get("key")
					// We should either get valid data or an error, but never corrupted data
					if err == nil {
						assert.NotEmpty(t, result.Value)
					}
				}
			}()
		}

		// Spawn writers
		for i := 0; i < numWriters; i++ {
			go func(id int) {
				defer wg.Done()
				for j := 0; j < 50; j++ {
					data := testData{Value: fmt.Sprintf("writer-%d-iteration-%d", id, j), Counter: j}
					err := cache.Set("key", data, time.Hour)
					assert.NoError(t, err)
				}
			}(i)
		}

		wg.Wait()

		// Final read should succeed with valid data
		result, err := cache.Get("key")
		require.NoError(t, err)
		assert.NotEmpty(t, result.Value)
	})
}

func TestClear(t *testing.T) {
	cache := filecache.New[testData]("test-domain", filecache.WithCacheDir[testData](t.TempDir()))

	// Add some data
	err := cache.Set("key1", testData{Value: "value1", Counter: 1}, time.Hour)
	require.NoError(t, err)
	err = cache.Set("key2", testData{Value: "value2", Counter: 2}, time.Hour)
	require.NoError(t, err)

	// Clear the cache
	err = cache.Clear()
	require.NoError(t, err)

	// Data should be gone
	_, err = cache.Get("key1")
	assert.True(t, filecache.IsCacheMiss(err))
	_, err = cache.Get("key2")
	assert.True(t, filecache.IsCacheMiss(err))
}
