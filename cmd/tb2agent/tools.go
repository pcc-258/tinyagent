package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/pcc-258/tinyagent/core"
	"github.com/pcc-258/tinyagent/tool"
)

const (
	maxToolOutput   = 96 * 1024
	maxSearchResult = 200
)

type shellArgs struct {
	Command    string `json:"command" desc:"Shell command to run with /bin/sh -lc"`
	Cwd        string `json:"cwd,omitempty" desc:"Working directory; defaults to the agent workdir"`
	TimeoutSec int    `json:"timeout_sec,omitempty" desc:"Command timeout in seconds"`
}

type readFileArgs struct {
	Path      string `json:"path" desc:"File path to read"`
	StartLine int    `json:"start_line,omitempty" desc:"1-based first line; default 1"`
	EndLine   int    `json:"end_line,omitempty" desc:"Inclusive last line; 0 reads to EOF"`
}

type writeFileArgs struct {
	Path    string `json:"path" desc:"File path to create or overwrite"`
	Content string `json:"content" desc:"Full file content"`
}

type listDirArgs struct {
	Path string `json:"path" desc:"Directory to list"`
}

type grepArgs struct {
	Pattern    string `json:"pattern" desc:"Regular expression to search"`
	Root       string `json:"root,omitempty" desc:"Directory to search recursively; defaults to the agent workdir"`
	MaxMatches int    `json:"max_matches,omitempty" desc:"Maximum matches; default 200"`
}

type findFilesArgs struct {
	Root       string `json:"root,omitempty" desc:"Directory to search recursively; defaults to the agent workdir"`
	Pattern    string `json:"pattern" desc:"Filename glob, e.g. *.go"`
	MaxResults int    `json:"max_results,omitempty" desc:"Maximum results; default 200"`
}

func newCodingTools(workdir string) ([]tool.Tool, error) {
	var tools []tool.Tool

	shellTool, err := tool.NewFuncTool("run_shell", "Run a shell command and return its exit code plus stdout/stderr",
		func(ctx context.Context, args shellArgs) (core.ToolResult, error) {
			return runShell(ctx, workdir, args)
		})
	if err != nil {
		return nil, err
	}
	tools = append(tools, shellTool)

	readTool, err := tool.NewFuncTool("read_file", "Read a text file with optional line range",
		func(_ context.Context, args readFileArgs) (core.ToolResult, error) {
			return readFile(args)
		})
	if err != nil {
		return nil, err
	}
	tools = append(tools, readTool)

	writeTool, err := tool.NewFuncTool("write_file", "Create or overwrite a text file",
		func(_ context.Context, args writeFileArgs) (core.ToolResult, error) {
			return writeFile(args)
		})
	if err != nil {
		return nil, err
	}
	tools = append(tools, writeTool)

	listTool, err := tool.NewFuncTool("list_dir", "List a directory with entry type and size",
		func(_ context.Context, args listDirArgs) (core.ToolResult, error) {
			return listDir(args)
		})
	if err != nil {
		return nil, err
	}
	tools = append(tools, listTool)

	grepTool, err := tool.NewFuncTool("grep_search", "Search files recursively with a regular expression",
		func(_ context.Context, args grepArgs) (core.ToolResult, error) {
			return grepSearch(workdir, args)
		})
	if err != nil {
		return nil, err
	}
	tools = append(tools, grepTool)

	findTool, err := tool.NewFuncTool("find_files", "Find files by filename glob under a directory",
		func(_ context.Context, args findFilesArgs) (core.ToolResult, error) {
			return findFiles(workdir, args)
		})
	if err != nil {
		return nil, err
	}
	tools = append(tools, findTool)

	return tools, nil
}

func runShell(ctx context.Context, baseDir string, args shellArgs) (core.ToolResult, error) {
	if strings.TrimSpace(args.Command) == "" {
		return core.ToolResult{Error: "command is required"}, nil
	}
	dir := args.Cwd
	if dir == "" {
		dir = baseDir
	}

	runCtx := ctx
	var cancel context.CancelFunc
	if args.TimeoutSec > 0 {
		runCtx, cancel = context.WithTimeout(ctx, time.Duration(args.TimeoutSec)*time.Second)
		defer cancel()
	}

	cmd := exec.CommandContext(runCtx, "/bin/sh", "-lc", args.Command)
	cmd.Dir = dir
	stdoutFile, err := os.CreateTemp("", "tb2agent-stdout-*")
	if err != nil {
		return core.ToolResult{Error: fmt.Sprintf("create stdout file: %v", err)}, nil
	}
	defer os.Remove(stdoutFile.Name())
	defer stdoutFile.Close()

	stderrFile, err := os.CreateTemp("", "tb2agent-stderr-*")
	if err != nil {
		return core.ToolResult{Error: fmt.Sprintf("create stderr file: %v", err)}, nil
	}
	defer os.Remove(stderrFile.Name())
	defer stderrFile.Close()

	cmd.Stdout = stdoutFile
	cmd.Stderr = stderrFile

	err = cmd.Run()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else if runCtx.Err() != nil {
			stdout, _ := os.ReadFile(stdoutFile.Name())
			stderr, _ := os.ReadFile(stderrFile.Name())
			return core.ToolResult{Content: fmt.Sprintf(
				"command timed out after %s\n--- stdout ---\n%s\n--- stderr ---\n%s",
				time.Duration(args.TimeoutSec)*time.Second,
				truncateOutput(string(stdout)),
				truncateOutput(string(stderr)),
			)}, nil
		} else {
			return core.ToolResult{Error: fmt.Sprintf("failed to start command: %v", err)}, nil
		}
	}

	stdout, _ := os.ReadFile(stdoutFile.Name())
	stderr, _ := os.ReadFile(stderrFile.Name())
	content := fmt.Sprintf(
		"exit_code=%d\n--- stdout ---\n%s\n--- stderr ---\n%s",
		exitCode,
		truncateOutput(string(stdout)),
		truncateOutput(string(stderr)),
	)
	return core.ToolResult{Content: content}, nil
}

func readFile(args readFileArgs) (core.ToolResult, error) {
	if args.Path == "" {
		return core.ToolResult{Error: "path is required"}, nil
	}
	f, err := os.Open(args.Path)
	if err != nil {
		return core.ToolResult{Error: err.Error()}, nil
	}
	defer f.Close()

	start := args.StartLine
	if start < 1 {
		start = 1
	}
	end := args.EndLine

	var sb strings.Builder
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		if line < start {
			continue
		}
		if end > 0 && line > end {
			break
		}
		fmt.Fprintf(&sb, "%6d | %s\n", line, scanner.Text())
		if sb.Len() > maxToolOutput {
			sb.WriteString("... output truncated ...\n")
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return core.ToolResult{Error: fmt.Sprintf("read %s: %v", args.Path, err)}, nil
	}
	if line < start {
		return core.ToolResult{Content: fmt.Sprintf("file has %d lines\n", line)}, nil
	}
	return core.ToolResult{Content: sb.String()}, nil
}

func writeFile(args writeFileArgs) (core.ToolResult, error) {
	if args.Path == "" {
		return core.ToolResult{Error: "path is required"}, nil
	}
	if err := os.MkdirAll(filepath.Dir(args.Path), 0o755); err != nil {
		return core.ToolResult{Error: err.Error()}, nil
	}
	if err := os.WriteFile(args.Path, []byte(args.Content), 0o644); err != nil {
		return core.ToolResult{Error: err.Error()}, nil
	}
	return core.ToolResult{Content: fmt.Sprintf("wrote %d bytes to %s", len(args.Content), args.Path)}, nil
}

func listDir(args listDirArgs) (core.ToolResult, error) {
	path := args.Path
	if path == "" {
		path = "."
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return core.ToolResult{Error: err.Error()}, nil
	}
	var sb strings.Builder
	for _, e := range entries {
		kind := "file"
		if e.IsDir() {
			kind = "dir"
		}
		size := int64(0)
		if info, err := e.Info(); err == nil {
			size = info.Size()
		}
		fmt.Fprintf(&sb, "%s\t%s\t%d\n", e.Name(), kind, size)
	}
	return core.ToolResult{Content: sb.String()}, nil
}

func grepSearch(baseDir string, args grepArgs) (core.ToolResult, error) {
	if args.Pattern == "" {
		return core.ToolResult{Error: "pattern is required"}, nil
	}
	re, err := regexp.Compile(args.Pattern)
	if err != nil {
		return core.ToolResult{Error: fmt.Sprintf("invalid pattern: %v", err)}, nil
	}
	root := args.Root
	if root == "" {
		root = baseDir
	}
	maxMatches := args.MaxMatches
	if maxMatches <= 0 {
		maxMatches = maxSearchResult
	}

	var sb strings.Builder
	count := 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if count >= maxMatches {
			return fs.SkipAll
		}

		f, openErr := os.Open(path)
		if openErr != nil {
			return nil
		}
		defer f.Close()

		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		lineNo := 0
		for scanner.Scan() {
			lineNo++
			text := scanner.Text()
			if !re.MatchString(text) {
				continue
			}
			fmt.Fprintf(&sb, "%s:%d:%s\n", path, lineNo, truncateLine(text))
			count++
			if count >= maxMatches {
				return fs.SkipAll
			}
		}
		return nil
	})
	if errors.Is(err, fs.SkipAll) {
		err = nil
	}
	if err != nil {
		return core.ToolResult{Error: err.Error()}, nil
	}
	if count == 0 {
		return core.ToolResult{Content: "no matches\n"}, nil
	}
	return core.ToolResult{Content: sb.String()}, nil
}

func findFiles(baseDir string, args findFilesArgs) (core.ToolResult, error) {
	if args.Pattern == "" {
		return core.ToolResult{Error: "pattern is required"}, nil
	}
	root := args.Root
	if root == "" {
		root = baseDir
	}
	maxResults := args.MaxResults
	if maxResults <= 0 {
		maxResults = maxSearchResult
	}

	var matches []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		ok, err := filepath.Match(args.Pattern, d.Name())
		if err != nil {
			return err
		}
		if ok {
			matches = append(matches, path)
			if len(matches) >= maxResults {
				return fs.SkipAll
			}
		}
		return nil
	})
	if errors.Is(err, fs.SkipAll) {
		err = nil
	}
	if err != nil {
		return core.ToolResult{Error: err.Error()}, nil
	}
	if len(matches) == 0 {
		return core.ToolResult{Content: "no files matched\n"}, nil
	}
	return core.ToolResult{Content: strings.Join(matches, "\n") + "\n"}, nil
}

func truncateOutput(s string) string {
	if len(s) <= maxToolOutput {
		return s
	}
	half := maxToolOutput / 2
	return s[:half] + "\n...[truncated]...\n" + s[len(s)-half:]
}

func truncateLine(s string) string {
	const maxLine = 240
	if len(s) <= maxLine {
		return s
	}
	return s[:maxLine] + "..."
}
