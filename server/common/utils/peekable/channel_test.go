package peekable

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPeekableChannel_PushPop_Basic(t *testing.T) {
	pc := NewPeekableChannel[int](3)

	val1 := 1
	val2 := 2
	val3 := 3

	err := pc.PushNonBlocking(&val1)
	require.NoError(t, err)
	err = pc.PushNonBlocking(&val2)
	require.NoError(t, err)
	err = pc.PushNonBlocking(&val3)
	require.NoError(t, err)

	item, wasPeeked := pc.PopNonBlocking()
	require.NotNil(t, item)
	assert.Equal(t, 1, *item)
	assert.False(t, wasPeeked)

	item, wasPeeked = pc.PopNonBlocking()
	require.NotNil(t, item)
	assert.Equal(t, 2, *item)
	assert.False(t, wasPeeked)

	item, wasPeeked = pc.PopNonBlocking()
	require.NotNil(t, item)
	assert.Equal(t, 3, *item)
	assert.False(t, wasPeeked)

	item, _ = pc.PopNonBlocking()
	assert.Nil(t, item)
}

func TestPeekableChannel_PushNonBlocking_QueueFull(t *testing.T) {
	pc := NewPeekableChannel[string](2)

	val1 := "first"
	val2 := "second"
	val3 := "third"

	err := pc.PushNonBlocking(&val1)
	require.NoError(t, err)
	err = pc.PushNonBlocking(&val2)
	require.NoError(t, err)

	err = pc.PushNonBlocking(&val3)
	require.Error(t, err)
	assert.Equal(t, ErrQueueFull, err)
}

func TestPeekableChannel_PushNonBlocking_AfterClose(t *testing.T) {
	pc := NewPeekableChannel[int](1)
	pc.Close()

	val := 1
	err := pc.PushNonBlocking(&val)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrQueueClosed)
}

func TestPeekableChannel_PushBlocking_Basic(t *testing.T) {
	pc := NewPeekableChannel[int](1)
	val := 42

	err := pc.PushBlocking(context.Background(), &val)
	require.NoError(t, err)

	item, wasPeeked := pc.PopNonBlocking()
	require.NotNil(t, item)
	assert.Equal(t, 42, *item)
	assert.False(t, wasPeeked)
}

func TestPeekableChannel_PushBlocking_AfterClose(t *testing.T) {
	pc := NewPeekableChannel[int](1)
	pc.Close()

	val := 1
	err := pc.PushBlocking(context.Background(), &val)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrQueueClosed)
}

func TestPeekableChannel_PushBlocking_ContextTimeoutWhenFull(t *testing.T) {
	pc := NewPeekableChannel[int](1)
	first := 1
	require.NoError(t, pc.PushNonBlocking(&first))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	second := 2
	start := time.Now()
	err := pc.PushBlocking(ctx, &second)
	duration := time.Since(start)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.GreaterOrEqual(t, duration, 10*time.Millisecond)
}

func TestPeekableChannel_PushBlocking_CloseWhileWaiting(t *testing.T) {
	pc := NewPeekableChannel[int](1)
	first := 1
	require.NoError(t, pc.PushNonBlocking(&first))

	errCh := make(chan error, 1)
	go func() {
		second := 2
		errCh <- pc.PushBlocking(context.Background(), &second)
	}()

	time.Sleep(10 * time.Millisecond)
	pc.Close()

	select {
	case err := <-errCh:
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrQueueClosed)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("PushBlocking should return after close")
	}
}

func TestPeekableChannel_PopNonBlocking_EmptyQueue(t *testing.T) {
	pc := NewPeekableChannel[int](3)

	item, wasPeeked := pc.PopNonBlocking()
	assert.Nil(t, item)
	assert.False(t, wasPeeked)
}

func TestPeekableChannel_PeekBlocking_Basic(t *testing.T) {
	pc := NewPeekableChannel[int](3)
	val := 42

	err := pc.PushNonBlocking(&val)
	require.NoError(t, err)

	ctx := context.Background()

	peeked, ok := pc.PeekBlocking(ctx)
	require.True(t, ok)
	require.NotNil(t, peeked)
	assert.Equal(t, 42, *peeked)

	peeked2, ok := pc.PeekBlocking(ctx)
	require.True(t, ok)
	require.NotNil(t, peeked2)
	assert.Equal(t, 42, *peeked2)

	item, wasPeeked := pc.PopNonBlocking()
	require.NotNil(t, item)
	assert.Equal(t, 42, *item)
	assert.True(t, wasPeeked, "should indicate this was a peeked value")

	item, _ = pc.PopNonBlocking()
	assert.Nil(t, item)
}

func TestPeekableChannel_PeekThenPop(t *testing.T) {
	pc := NewPeekableChannel[string](3)

	val1 := "first"
	val2 := "second"

	err := pc.PushNonBlocking(&val1)
	require.NoError(t, err)
	err = pc.PushNonBlocking(&val2)
	require.NoError(t, err)

	ctx := context.Background()

	peeked, ok := pc.PeekBlocking(ctx)
	require.True(t, ok)
	assert.Equal(t, "first", *peeked)

	item, wasPeeked := pc.PopNonBlocking()
	require.NotNil(t, item)
	assert.Equal(t, "first", *item)
	assert.True(t, wasPeeked)

	item, wasPeeked = pc.PopNonBlocking()
	require.NotNil(t, item)
	assert.Equal(t, "second", *item)
	assert.False(t, wasPeeked)
}

func TestPeekableChannel_PeekBlocking_ContextTimeout(t *testing.T) {
	pc := NewPeekableChannel[int](3)

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	peeked, ok := pc.PeekBlocking(ctx)
	duration := time.Since(start)

	assert.False(t, ok)
	assert.Nil(t, peeked)
	assert.GreaterOrEqual(t, duration, 10*time.Millisecond, "should have waited for timeout")
}

func TestPeekableChannel_PeekBlocking_ContextCanceled(t *testing.T) {
	pc := NewPeekableChannel[int](3)

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	peeked, ok := pc.PeekBlocking(ctx)
	duration := time.Since(start)

	assert.False(t, ok)
	assert.Nil(t, peeked)
	assert.GreaterOrEqual(t, duration, 10*time.Millisecond)
	assert.Less(t, duration, 100*time.Millisecond, "should return quickly after cancel")
}

func TestPeekableChannel_PeekBlocking_ValueBeforeTimeout(t *testing.T) {
	pc := NewPeekableChannel[int](3)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	val := 42

	go func() {
		time.Sleep(10 * time.Millisecond)
		err := pc.PushNonBlocking(&val)
		require.NoError(t, err)
	}()

	start := time.Now()
	peeked, ok := pc.PeekBlocking(ctx)
	duration := time.Since(start)

	require.True(t, ok)
	require.NotNil(t, peeked)
	assert.Equal(t, 42, *peeked)
	assert.Less(t, duration, 100*time.Millisecond, "should receive value before timeout")
}

func TestPeekableChannel_ConcurrentPushPop(t *testing.T) {
	pc := NewPeekableChannel[int](100)
	const numGoroutines = 10
	const itemsPerGoroutine = 100

	var wg sync.WaitGroup

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(offset int) {
			defer wg.Done()
			for j := 0; j < itemsPerGoroutine; j++ {
				val := offset*itemsPerGoroutine + j
				for {
					err := pc.PushNonBlocking(&val)
					if err == nil {
						break
					}
					time.Sleep(time.Microsecond)
				}
			}
		}(i)
	}

	poppedItems := make([]int, 0, numGoroutines*itemsPerGoroutine)
	var mu sync.Mutex

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < itemsPerGoroutine; j++ {
				for {
					item, _ := pc.PopNonBlocking()
					if item != nil {
						mu.Lock()
						poppedItems = append(poppedItems, *item)
						mu.Unlock()
						break
					}
					time.Sleep(time.Microsecond)
				}
			}
		}()
	}

	wg.Wait()

	assert.Equal(t, numGoroutines*itemsPerGoroutine, len(poppedItems))
}

func TestPeekableChannel_PointerValues(t *testing.T) {
	type TestStruct struct {
		Name string
		Age  int
	}

	pc := NewPeekableChannel[TestStruct](3)

	val1 := TestStruct{Name: "Alice", Age: 30}
	val2 := TestStruct{Name: "Bob", Age: 25}

	err := pc.PushNonBlocking(&val1)
	require.NoError(t, err)
	err = pc.PushNonBlocking(&val2)
	require.NoError(t, err)

	ctx := context.Background()

	peeked, ok := pc.PeekBlocking(ctx)
	require.True(t, ok)
	assert.Equal(t, "Alice", peeked.Name)
	assert.Equal(t, 30, peeked.Age)

	item, wasPeeked := pc.PopNonBlocking()
	require.NotNil(t, item)
	assert.True(t, wasPeeked)
	assert.Equal(t, "Alice", item.Name)

	item, wasPeeked = pc.PopNonBlocking()
	require.NotNil(t, item)
	assert.False(t, wasPeeked)
	assert.Equal(t, "Bob", item.Name)
}

func TestPeekableChannel_MultipleConsecutivePeeks(t *testing.T) {
	pc := NewPeekableChannel[int](3)

	val1 := 10
	val2 := 20

	err := pc.PushNonBlocking(&val1)
	require.NoError(t, err)
	err = pc.PushNonBlocking(&val2)
	require.NoError(t, err)

	ctx := context.Background()

	for i := 0; i < 5; i++ {
		peeked, ok := pc.PeekBlocking(ctx)
		require.True(t, ok)
		assert.Equal(t, 10, *peeked, "should always return the same first value")
	}

	item, wasPeeked := pc.PopNonBlocking()
	require.NotNil(t, item)
	assert.Equal(t, 10, *item)
	assert.True(t, wasPeeked)

	peeked, ok := pc.PeekBlocking(ctx)
	require.True(t, ok)
	assert.Equal(t, 20, *peeked)
}

func TestPeekableChannel_PopAfterPeek_DoesNotSkip(t *testing.T) {
	pc := NewPeekableChannel[int](5)

	for i := 1; i <= 3; i++ {
		val := i
		err := pc.PushNonBlocking(&val)
		require.NoError(t, err)
	}

	ctx := context.Background()

	peeked, ok := pc.PeekBlocking(ctx)
	require.True(t, ok)
	assert.Equal(t, 1, *peeked)

	item, wasPeeked := pc.PopNonBlocking()
	require.NotNil(t, item)
	assert.Equal(t, 1, *item)
	assert.True(t, wasPeeked)

	item, wasPeeked = pc.PopNonBlocking()
	require.NotNil(t, item)
	assert.Equal(t, 2, *item)
	assert.False(t, wasPeeked)

	item, wasPeeked = pc.PopNonBlocking()
	require.NotNil(t, item)
	assert.Equal(t, 3, *item)
	assert.False(t, wasPeeked)

	item, _ = pc.PopNonBlocking()
	assert.Nil(t, item)
}

func TestPeekableChannel_ZeroSizeQueue(t *testing.T) {
	pc := NewPeekableChannel[int](0)

	val := 42
	err := pc.PushNonBlocking(&val)
	require.Error(t, err)
	assert.Equal(t, ErrQueueFull, err)
}

func TestPeekableChannel_Close(t *testing.T) {
	pc := NewPeekableChannel[int](3)

	val := 42
	err := pc.PushNonBlocking(&val)
	require.NoError(t, err)

	pc.Close()

	ctx := context.Background()
	item, ok := pc.PeekBlocking(ctx)
	assert.False(t, ok)
	assert.Nil(t, item)

	item2, wasPeeked := pc.PopNonBlocking()
	require.NotNil(t, item2)
	assert.Equal(t, 42, *item2)
	assert.False(t, wasPeeked)
}

func TestPeekableChannel_PopBlocking_Basic(t *testing.T) {
	pc := NewPeekableChannel[int](3)

	val1 := 1
	val2 := 2
	val3 := 3

	err := pc.PushNonBlocking(&val1)
	require.NoError(t, err)
	err = pc.PushNonBlocking(&val2)
	require.NoError(t, err)
	err = pc.PushNonBlocking(&val3)
	require.NoError(t, err)

	ctx := context.Background()

	item, ok := pc.PopBlocking(ctx)
	require.True(t, ok)
	require.NotNil(t, item)
	assert.Equal(t, 1, *item)

	item, ok = pc.PopBlocking(ctx)
	require.True(t, ok)
	require.NotNil(t, item)
	assert.Equal(t, 2, *item)

	item, ok = pc.PopBlocking(ctx)
	require.True(t, ok)
	require.NotNil(t, item)
	assert.Equal(t, 3, *item)
}

func TestPeekableChannel_PopBlocking_ContextTimeout(t *testing.T) {
	pc := NewPeekableChannel[int](3)

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	item, ok := pc.PopBlocking(ctx)
	duration := time.Since(start)

	assert.False(t, ok)
	assert.Nil(t, item)
	assert.GreaterOrEqual(t, duration, 10*time.Millisecond, "should have waited for timeout")
}

func TestPeekableChannel_PopBlocking_ContextCanceled(t *testing.T) {
	pc := NewPeekableChannel[int](3)

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	item, ok := pc.PopBlocking(ctx)
	duration := time.Since(start)

	assert.False(t, ok)
	assert.Nil(t, item)
	assert.GreaterOrEqual(t, duration, 10*time.Millisecond)
	assert.Less(t, duration, 100*time.Millisecond, "should return quickly after cancel")
}

func TestPeekableChannel_PopBlocking_ValueBeforeTimeout(t *testing.T) {
	pc := NewPeekableChannel[int](3)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	val := 42

	go func() {
		time.Sleep(10 * time.Millisecond)
		err := pc.PushNonBlocking(&val)
		require.NoError(t, err)
	}()

	start := time.Now()
	item, ok := pc.PopBlocking(ctx)
	duration := time.Since(start)

	require.True(t, ok)
	require.NotNil(t, item)
	assert.Equal(t, 42, *item)
	assert.Less(t, duration, 100*time.Millisecond, "should receive value before timeout")
}

func TestPeekableChannel_PopBlocking_AfterPeek(t *testing.T) {
	pc := NewPeekableChannel[int](3)

	val := 42
	err := pc.PushNonBlocking(&val)
	require.NoError(t, err)

	ctx := context.Background()

	peeked, ok := pc.PeekBlocking(ctx)
	require.True(t, ok)
	require.NotNil(t, peeked)
	assert.Equal(t, 42, *peeked)

	item, ok := pc.PopBlocking(ctx)
	require.True(t, ok)
	require.NotNil(t, item)
	assert.Equal(t, 42, *item)

	timeoutCtx, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancel()
	item, ok = pc.PopBlocking(timeoutCtx)
	assert.False(t, ok)
	assert.Nil(t, item)
}

func TestPeekableChannel_PopBlocking_Close(t *testing.T) {
	pc := NewPeekableChannel[int](3)

	pc.Close()

	ctx := context.Background()
	item, ok := pc.PopBlocking(ctx)
	assert.False(t, ok)
	assert.Nil(t, item)
}
