package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunShell(t *testing.T) {
	res, err := runShell(context.Background(), t.TempDir(), shellArgs{
		Command: "printf 'hello' && exit 3",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, "exit_code=3") {
		t.Errorf("content = %q, want exit_code=3", res.Content)
	}
	if !strings.Contains(res.Content, "hello") {
		t.Errorf("content = %q, want captured stdout", res.Content)
	}
}

func TestWriteReadFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "a.txt")
	written, err := writeFile(writeFileArgs{Path: path, Content: "line1\nline2\nline3\n"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(written.Content, "wrote 18 bytes") {
		t.Errorf("write result = %q", written.Content)
	}

	read, err := readFile(readFileArgs{Path: path, StartLine: 2, EndLine: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read.Content, "line2") || strings.Contains(read.Content, "line1") {
		t.Errorf("read result = %q, want only line2", read.Content)
	}
}

func TestGrepAndFind(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n// TODO fix\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# readme\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	grep, err := grepSearch(dir, grepArgs{Pattern: "TODO", Root: dir})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(grep.Content, "main.go") {
		t.Errorf("grep result = %q, want main.go match", grep.Content)
	}

	found, err := findFiles(dir, findFilesArgs{Root: dir, Pattern: "*.go"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(found.Content, "main.go") || strings.Contains(found.Content, "README.md") {
		t.Errorf("find result = %q, want only main.go", found.Content)
	}
}

// TestRunShellBackgroundProcessDoesNotBlock ensures a command that starts a
// background process returns immediately instead of waiting for the inherited
// output pipe to close.
func TestRunShellBackgroundProcessDoesNotBlock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := runShell(ctx, t.TempDir(), shellArgs{Command: "sleep 5 & echo started"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Content, "command timed out") {
		t.Fatalf("background command blocked runShell: %s", res.Content)
	}
	if !strings.Contains(res.Content, "started") {
		t.Errorf("content = %q, want captured stdout", res.Content)
	}
}

func TestRunShellTimeoutReturnsPartialOutput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := runShell(ctx, t.TempDir(), shellArgs{
		Command:    "echo before; sleep 2; echo after",
		TimeoutSec: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, "command timed out") {
		t.Fatalf("content = %q, want timeout message", res.Content)
	}
	if !strings.Contains(res.Content, "before") {
		t.Errorf("content = %q, want partial stdout", res.Content)
	}
}

func TestRunShellTruncatesOversizedOutput(t *testing.T) {
	res, err := runShell(context.Background(), t.TempDir(), shellArgs{
		Command: "python3 -c \"import sys; sys.stdout.write('x'*200000)\"",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, "truncated") {
		t.Errorf("content length = %d, want truncation marker", len(res.Content))
	}
	if len(res.Content) > maxToolOutput+1000 {
		t.Errorf("content length = %d, want bounded output", len(res.Content))
	}
}

func TestWriteFileBadPathIsSoftError(t *testing.T) {
	res, err := writeFile(writeFileArgs{
		Path:    "/nonexistent-tinyagent-dir/a.txt",
		Content: "x",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Error == "" {
		t.Fatal("expected soft error for unwritable path")
	}
}
