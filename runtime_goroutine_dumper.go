// Package runtimegoroutinedumper captures and archives goroutine stack traces
// on signal or interval to a rotating file for post-mortem analysis.
package runtimegoroutinedumper

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// DefaultStackSize is the size of the buffer used to capture goroutine stacks.
// 1 MiB is enough for a few thousand goroutines and keeps memory bounded.
const DefaultStackSize = 1 << 20

// DefaultMaxFiles is the number of rotated dump files kept on disk.
const DefaultMaxFiles = 5

// Dumper captures goroutine stack traces and writes them to a rotating file.
//
// A Dumper is safe for concurrent use. Dump may be called from multiple
// goroutines; writes are serialized with a mutex.
type Dumper struct {
	mu       sync.Mutex
	path     string
	maxFiles int
	stackBuf []byte
	clock    func() time.Time
	idGen    func() uint64
	nextID   uint64
}

// New returns a Dumper that writes dumps to path and keeps at most maxFiles
// rotated files. If maxFiles is <= 0, DefaultMaxFiles is used. If clock is nil,
// time.Now is used. If idGen is nil, a monotonic counter is used.
//
// The path is not created until the first Dump call.
func New(path string, maxFiles int, clock func() time.Time, idGen func() uint64) (*Dumper, error) {
	if path == "" {
		return nil, errors.New("runtimegoroutinedumper: empty path")
	}
	if maxFiles <= 0 {
		maxFiles = DefaultMaxFiles
	}
	if clock == nil {
		clock = time.Now
	}
	d := &Dumper{
		path:     path,
		maxFiles: maxFiles,
		stackBuf: make([]byte, DefaultStackSize),
		clock:    clock,
		idGen:    idGen,
	}
	return d, nil
}

// Dump captures the current goroutine stack traces and writes them to the
// configured path. It also rotates old files so that at most maxFiles remain.
//
// Dump returns the number of bytes written and any error encountered.
func (d *Dumper) Dump() (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	n := runtime.Stack(d.stackBuf, true)
	if n == 0 {
		return 0, errors.New("runtimegoroutinedumper: runtime.Stack returned 0 bytes")
	}

	id := d.nextID
	if d.idGen != nil {
		id = d.idGen()
	}
	d.nextID++

	stamp := d.clock().UTC().Format("20060102T150405.000000000Z")
	name := fmt.Sprintf("%s.%s.%d", d.path, stamp, id)

	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return 0, fmt.Errorf("runtimegoroutinedumper: mkdir: %w", err)
	}

	f, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return 0, fmt.Errorf("runtimegoroutinedumper: open: %w", err)
	}

	written, werr := f.Write(d.stackBuf[:n])
	cerr := f.Close()
	if werr != nil {
		return written, fmt.Errorf("runtimegoroutinedumper: write: %w", werr)
	}
	if cerr != nil {
		return written, fmt.Errorf("runtimegoroutinedumper: close: %w", cerr)
	}

	if err := d.rotate(); err != nil {
		return written, fmt.Errorf("runtimegoroutinedumper: rotate: %w", err)
	}
	return written, nil
}

// rotate removes the oldest dump files so that at most maxFiles remain.
// Files are matched by the configured path prefix followed by a dot.
func (d *Dumper) rotate() error {
	dir := filepath.Dir(d.path)
	base := filepath.Base(d.path)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	prefix := base + "."
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		names = append(names, name)
	}

	sort.Strings(names)
	if len(names) <= d.maxFiles {
		return nil
	}

	toRemove := names[:len(names)-d.maxFiles]
	for _, name := range toRemove {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	return nil
}

// Start installs a signal handler for SIGQUIT and SIGUSR1 and starts a
// goroutine that dumps on each signal. It returns a function that stops the
// handler and waits for the goroutine to exit.
//
// The returned stop function is safe to call more than once.
func (d *Dumper) Start() (stop func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGQUIT, syscall.SIGUSR1)

	done := make(chan struct{})
	var once sync.Once

	go func() {
		defer close(done)
		for range ch {
			_, _ = d.Dump()
		}
	}()

	return func() {
		once.Do(func() {
			signal.Stop(ch)
			close(ch)
			<-done
		})
	}
}

// StartInterval starts a goroutine that calls Dump every interval. It returns
// a function that stops the ticker and waits for the goroutine to exit.
//
// If interval <= 0, StartInterval returns a no-op stop function and does not
// start a goroutine.
func (d *Dumper) StartInterval(interval time.Duration) (stop func()) {
	if interval <= 0 {
		return func() {}
	}

	ticker := time.NewTicker(interval)
	done := make(chan struct{})
	var once sync.Once

	go func() {
		defer close(done)
		for range ticker.C {
			_, _ = d.Dump()
		}
	}()

	return func() {
		once.Do(func() {
			ticker.Stop()
			<-done
		})
	}
}

// WriteTo writes the current goroutine stack traces to w. It is a convenience
// for callers that want the stack without touching the filesystem.
func WriteTo(w io.Writer) (int, error) {
	buf := make([]byte, DefaultStackSize)
	n := runtime.Stack(buf, true)
	if n == 0 {
		return 0, errors.New("runtimegoroutinedumper: runtime.Stack returned 0 bytes")
	}
	return w.Write(buf[:n])
}

// ParseID extracts the numeric id suffix from a dump file name produced by
// Dump. It returns an error if the name does not match the expected shape.
func ParseID(name string) (uint64, error) {
	i := strings.LastIndexByte(name, '.')
	if i < 0 || i == len(name)-1 {
		return 0, fmt.Errorf("runtimegoroutinedumper: no id in %q", name)
	}
	id, err := strconv.ParseUint(name[i+1:], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("runtimegoroutinedumper: bad id in %q: %w", name, err)
	}
	return id, nil
}
