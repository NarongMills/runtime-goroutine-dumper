# runtime-goroutine-dumper

Captures and archives goroutine stack traces on signal or interval to a rotating file for post-mortem analysis.

## Usage

```go
package main

import (
	"log"
	"time"

	dumper "runtimegoroutinedumper"
)

func main() {
	d, err := dumper.New("/var/log/myapp/goroutines", 5, nil, nil)
	if err != nil {
		log.Fatal(err)
	}

	// Dump on SIGQUIT and SIGUSR1.
	stopSignal := d.Start()
	defer stopSignal()

	// Also dump every 30 seconds.
	stopInterval := d.StartInterval(30 * time.Second)
	defer stopInterval()

	select {}
}
```

## Why this exists

When a Go program wedges, the default `SIGQUIT` behaviour prints every goroutine
stack to stderr and exits. That is fine interactively, but in a container the
output goes to the log stream, is truncated, and is gone once the process is
restarted. This library writes the same stack dump to a file and keeps the last
few dumps, so the trace survives the process.

The trade-off is simplicity: there is no compression, no shipping to a remote
collector, and no locking against a second process writing to the same prefix.
It is a local, best-effort archive. If you need durable transport, read the
files from a sidecar.

## API

- `New(path string, maxFiles int, clock func() time.Time, idGen func() uint64) (*Dumper, error)`
  creates a dumper writing to `path`. Rotated files are named `path.<UTC timestamp>.<id>`.
  `maxFiles <= 0` uses `DefaultMaxFiles` (5). A nil `clock` uses `time.Now`; a nil
  `idGen` uses an internal counter. The path is not created until the first `Dump`.
- `(*Dumper) Dump() (int, error)` captures the stacks and writes one file, then
  removes the oldest files so at most `maxFiles` remain. It returns the number of
  bytes written.
- `(*Dumper) Start() (stop func())` installs handlers for `SIGQUIT` and `SIGUSR1`
  and dumps on each signal. The returned function stops the handler and waits for
  the goroutine to exit; it is safe to call more than once.
- `(*Dumper) StartInterval(interval time.Duration) (stop func())` dumps every
  `interval`. A non-positive interval is a no-op.
- `WriteTo(w io.Writer) (int, error)` writes the current stacks to `w` without
  touching the filesystem.
- `ParseID(name string) (uint64, error)` extracts the numeric id suffix from a
  dump file name.
- `DefaultStackSize` (1 MiB) and `DefaultMaxFiles` (5) are exported constants.

## Edge cases

Rotation is by lexicographic file name, not by reading file timestamps. The file
name embeds a zero-padded UTC timestamp and the id, so lexicographic order matches
creation order as long as the clock is monotonic and the id counter does not wrap.
If you supply a custom `idGen` that returns the same value twice, two dumps can
collide on the same file name and the second will overwrite the first. If you
supply a `clock` that goes backwards, rotation may remove the wrong file. Both are
documented here rather than defended against, because the default implementations
are monotonic and the custom hooks exist for tests.
