package tasklist

import (
	"testing"
	"time"

	"github.com/superdurable/dex/server/common/log"
	"github.com/stretchr/testify/require"
)

func TestPushBack_BlocksUntilBufferSpace(t *testing.T) {
	buffer := make(chan *Task, 1)
	buffer <- &Task{taskID: 1}

	reader := &taskReader{
		matcherBufferCh: buffer,
		stopCh:          make(chan struct{}),
		logger:          log.NewNoop(),
	}

	task := &Task{taskID: 42}
	done := make(chan struct{})
	go func() {
		reader.PushBack(task)
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("PushBack should block while buffer is full")
	case <-time.After(50 * time.Millisecond):
	}

	<-buffer

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("PushBack should complete after buffer space is available")
	}

	got := <-buffer
	require.Equal(t, int64(42), got.taskID)
}

func TestPushBack_ReturnsWhenReaderStops(t *testing.T) {
	buffer := make(chan *Task, 1)
	buffer <- &Task{taskID: 1}

	reader := &taskReader{
		matcherBufferCh: buffer,
		stopCh:          make(chan struct{}),
		logger:          log.NewNoop(),
	}

	task := &Task{taskID: 99}
	done := make(chan struct{})
	go func() {
		reader.PushBack(task)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	close(reader.stopCh)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("PushBack should unblock when reader stopCh closes")
	}

	got := <-buffer
	require.Equal(t, int64(1), got.taskID)
	select {
	case extra := <-buffer:
		t.Fatalf("task should not be pushed after reader stop, got task_id=%d", extra.taskID)
	default:
	}
}
