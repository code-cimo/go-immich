// Package logger provides an asynchronous, allocation-light request logger.
//
// Rationale (ODROID-C2 / eMMC storage): synchronous per-request log writes add
// I/O latency to every HTTP call and wear out cheap flash storage. Instead we
// push formatted log lines onto a buffered channel and drain them in a single
// background goroutine. If the channel is full we DROP lines rather than block
// request handlers - losing debug output is always preferable to stalling the
// upload pipeline on a 2GB board.
package logger

import (
	"context"
	"io"
	"log"
	"os"
	"sync"
)

const chanCapacity = 1024 // ~50KB of queued strings; bounded by design

// AsyncLogger serialises writes through a worker goroutine.
type AsyncLogger struct {
	out   io.Writer
	lines chan string
	wg    sync.WaitGroup
	once  sync.Once
}

var (
	mu       sync.Mutex
	instance *AsyncLogger
	stopCh   chan struct{} // closes the shared Default() drain loop
)

// New starts the draining goroutine bound to ctx. The logger stops as soon as
// ctx is cancelled (graceful shutdown drains any pending lines first).
func New(ctx context.Context, w io.Writer) *AsyncLogger {
	l := &AsyncLogger{out: w, lines: make(chan string, chanCapacity)}
	l.wg.Add(1)
	go l.drain(ctx, nil)
	return l
}

// drain is the single writer goroutine. It exits when either ctx is cancelled
// or stop is closed, flushing everything still queued before returning.
// Exactly one of the two cancellation sources is used per logger instance.
func (l *AsyncLogger) drain(ctx context.Context, stop <-chan struct{}) {
	defer l.wg.Done()
	for {
		select {
		case line, ok := <-l.lines:
			if !ok {
				return // channel closed by Close(); nothing left to flush
			}
			// prefix kept minimal: no timestamp/date flags (spec asks for
			// zero overhead); the request line itself carries latency.
			l.out.Write([]byte(line + "\n"))
		case <-ctx.Done():
			l.flush()
			return
		case <-stop:
			l.flush()
			return
		}
	}
}

// flush writes out every line still buffered in the channel (non-blocking).
func (l *AsyncLogger) flush() {
	for {
		select {
		case line, ok := <-l.lines:
			if !ok {
				return
			}
			l.out.Write([]byte(line + "\n"))
		default:
			return
		}
	}
}

// Log enqueues one line; never blocks the caller.
func (l *AsyncLogger) Log(line string) {
	if l == nil {
		return
	}
	select {
	case l.lines <- line:
	default: // channel saturated -> drop (protects request latency)
	}
}

// Close stops accepting new lines and waits for the queue to flush.
//
// For loggers created with New() we close the input channel (safe: each
// instance owns its channel). For the shared Default() singleton, closing the
// channel would deadlock future Log() calls in other code paths, so instead
// we signal the drain loop via stopCh; Log() then sees a saturated/abandoned
// queue and drops lines - the documented fail-open behaviour.
func (l *AsyncLogger) Close() {
	if l == nil {
		return
	}
	l.once.Do(func() {
		mu.Lock()
		isShared := l == instance
		mu.Unlock()
		if isShared {
			close(stopCh) // wake drain loop -> it flushes and exits
		} else {
			close(l.lines)
		}
	})
	l.wg.Wait()
}

// ---------- process-wide convenience (used before DI wiring exists) ----------

// Default returns the shared async logger writing to stderr. Standard library
// log output stays synchronous for start-up/fatal messages.
func Default() *AsyncLogger {
	mu.Lock()
	defer mu.Unlock()
	if instance == nil {
		instance = &AsyncLogger{out: os.Stderr, lines: make(chan string, chanCapacity)}
		stopCh = make(chan struct{})
		instance.wg.Add(1)
		go instance.drain(context.Background(), stopCh)
		log.SetFlags(log.Lmicroseconds) // boot logs only; request logs bypass stdlib
	}
	return instance
}
