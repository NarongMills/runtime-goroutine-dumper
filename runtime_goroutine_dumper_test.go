package runtimegoroutinedumper

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

func TestNewRejectsEmptyPath(t *testing.T) {
	if _, err := New("", 1, nil, nil); err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestNewDefaultsMaxFiles(t *testing.T) {
	d, err := New(filepath.Join(t.TempDir(), "dump"), 0, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.maxFiles != DefaultMaxFiles {
		t.Fatalf("maxFiles = %d, want %d", d.maxFiles, DefaultMaxFiles)
	}
}

func TestDumpWritesStack(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "dump")
	stamp := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	d, err := New(base, 3, fixedClock(stamp), nil)
	if err != nil {
		t.Fatal(err)
	}

	n, err := d.Dump()
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("expected non-zero bytes written")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d files, want 1", len(entries))
	}

	name := entries[0].Name()
	if !strings.HasPrefix(name, "dump.") {
		t.Fatalf("unexpected file name %q", name)
	}

	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "goroutine") {
		t.Fatalf("dump does not look like a stack trace: %q", data[:min(len(data), 80)])
	}
}

func TestDumpRotates(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "dump")
	stamp := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	d, err := New(base, 2, fixedClock(stamp), nil)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 4; i++ {
		if _, err := d.Dump(); err != nil {
			t.Fatal(err)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d files, want 2", len(entries))
	}

	// The two newest ids are 2 and 3.
	var ids []uint64
	for _, e := range entries {
		id, err := ParseID(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if ids[0] != 2 || ids[1] != 3 {
		t.Fatalf("ids = %v, want [2 3]", ids)
	}
}

func TestDumpUsesIDGen(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "dump")
	stamp := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	var counter uint64 = 100
	idGen := func() uint64 {
		counter++
		return counter
	}
	d, err := New(base, 3, fixedClock(stamp), idGen)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := d.Dump(); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d files, want 1", len(entries))
	}
	id, err := ParseID(entries[0].Name())
	if err != nil {
		t.Fatal(err)
	}
	if id != 101 {
		t.Fatalf("id = %d, want 101", id)
	}
}

func TestParseID(t *testing.T) {
	id, err := ParseID("dump.20240102T030405.000000000Z.7")
	if err != nil {
		t.Fatal(err)
	}
	if id != 7 {
		t.Fatalf("id = %d, want 7", id)
	}

	if _, err := ParseID("dump"); err == nil {
		t.Fatal("expected error for missing id")
	}
	if _, err := ParseID("dump."); err == nil {
		t.Fatal("expected error for trailing dot")
	}
	if _, err := ParseID("dump.abc"); err == nil {
		t.Fatal("expected error for non-numeric id")
	}
}

func TestWriteTo(t *testing.T) {
	var buf bytes.Buffer
	n, err := WriteTo(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("expected non-zero bytes")
	}
	if !strings.Contains(buf.String(), "goroutine") {
		t.Fatal("output does not look like a stack trace")
	}
}

func TestStartIntervalZeroIsNoop(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "dump")
	d, err := New(base, 3, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	stop := d.StartInterval(0)
	defer stop()

	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no files, got %d", len(entries))
	}
}

func TestStartStopIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "dump")
	d, err := New(base, 3, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	stop := d.Start()
	stop()
	stop()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
