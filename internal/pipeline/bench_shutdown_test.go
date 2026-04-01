package pipeline

import (
	"context"
	"testing"
	"time"
)

func BenchmarkStopIdleMemory(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		p := New(nil, 16, 1)
		p.Start(context.Background())
		b.StartTimer()
		p.Stop()
	}
}

func BenchmarkStopIdleDurable(b *testing.B) {
	setBenchmarkQueueTimings(b, time.Nanosecond, maxEnqueueWait, enqueueRetryInterval)

	queue := &benchmarkQueue{}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		p := NewDurable(nil, queue, 16, 1)
		p.Start(context.Background())
		b.StartTimer()
		p.Stop()
	}
}
