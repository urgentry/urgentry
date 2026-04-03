package pipeline

import (
	"context"
	"testing"
	"time"

	"urgentry/internal/sqlite"
)

func BenchmarkEnqueueNonBlockingMemory(b *testing.B) {
	p := New(nil, b.N+1, 1)
	item := benchmarkItem()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !p.EnqueueNonBlocking(item) {
			b.Fatal("enqueue returned false")
		}
	}
}

func BenchmarkEnqueueNonBlockingMemorySaturated(b *testing.B) {
	p := New(nil, 1, 1)
	item := benchmarkItem()
	if !p.EnqueueNonBlocking(item) {
		b.Fatal("prefill enqueue returned false")
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if p.EnqueueNonBlocking(item) {
			b.Fatal("enqueue returned true for a full in-memory queue")
		}
	}
}

func BenchmarkEnqueueNonBlockingDurable(b *testing.B) {
	db := benchmarkOpenDurableStore(b)
	p := NewDurable(nil, sqlite.NewJobStore(db), b.N+1, 1)
	item := benchmarkItem()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !p.EnqueueNonBlocking(item) {
			b.Fatal("enqueue returned false")
		}
	}
}

func BenchmarkEnqueueNonBlockingDurableSaturated(b *testing.B) {
	queue := &benchmarkQueue{
		enqueue: func(context.Context, string, string, []byte, int) (bool, error) {
			return false, nil
		},
	}
	p := NewDurable(nil, queue, 1, 1)
	item := benchmarkItem()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if p.EnqueueNonBlocking(item) {
			b.Fatal("enqueue returned true for a saturated durable queue")
		}
	}
}

func BenchmarkEnqueueDurableTimeout(b *testing.B) {
	silencePipelineLogs(b)

	queue := &benchmarkQueue{
		enqueue: func(context.Context, string, string, []byte, int) (bool, error) {
			return false, nil
		},
	}
	p := NewDurable(nil, queue, 1, 1)
	setBenchmarkQueueTimings(p, p.idlePollInterval, time.Nanosecond, 0)
	item := benchmarkItem()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if p.Enqueue(item) {
			b.Fatal("enqueue returned true for a queue that never accepted work")
		}
	}
}
