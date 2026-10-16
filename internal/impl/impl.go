// Package impl starts a DKF implementation from its command template and
// speaks MCP to it. The harness depends on nothing else an implementation
// provides.
package impl

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Placeholder is replaced by the absolute workspace path in a command.
const Placeholder = "{workspace}"

// Impl is an implementation under test: a label and a command template.
type Impl struct {
	Label   string `json:"label"`
	Command string `json:"command"`
}

// Parse reads "<label>=<command>"; the command must contain {workspace}.
func Parse(s string) (Impl, error) {
	label, cmd, ok := strings.Cut(s, "=")
	label, cmd = strings.TrimSpace(label), strings.TrimSpace(cmd)
	if !ok || label == "" || cmd == "" {
		return Impl{}, fmt.Errorf("--impl %q: want <label>=<command>", s)
	}
	if !strings.Contains(cmd, Placeholder) {
		return Impl{}, fmt.Errorf("--impl %s: command must contain %s", label, Placeholder)
	}
	return Impl{Label: label, Command: cmd}, nil
}

// ServerInfo identifies an implementation, from initialize.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// StartError is a server that failed before initialize completed.
type StartError struct {
	Command string
	Err     error
	Stderr  string
}

func (e *StartError) Error() string {
	msg := fmt.Sprintf("starting %q: %v", e.Command, e.Err)
	if s := strings.TrimSpace(e.Stderr); s != "" {
		msg += "\nstderr:\n" + s
	}
	return msg
}

// Server is a running implementation bound to one workspace.
type Server struct {
	Info         ServerInfo
	Instructions string
	Tools        []*mcp.Tool
	// Workspace is the absolute path substituted for {workspace}.
	Workspace string
	session   *mcp.ClientSession
	stderr    *syncBuffer
}

// Start runs impl against workspace: {workspace} becomes the absolute path,
// DKF_WORKSPACE is set to it, and so is the working directory. It completes
// initialize and tools/list.
func Start(ctx context.Context, im Impl, workspace string) (*Server, error) {
	abs, err := filepath.Abs(workspace)
	if err != nil {
		return nil, err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	command := strings.ReplaceAll(im.Command, Placeholder, shellQuote(abs))
	cmd := exec.Command("/bin/sh", "-c", command)
	cmd.Dir = abs
	cmd.Env = append(os.Environ(), "DKF_WORKSPACE="+abs)
	stderr := &syncBuffer{}
	cmd.Stderr = stderr

	client := mcp.NewClient(&mcp.Implementation{Name: "particulars-bench", Version: Version}, nil)
	transport := &mcp.CommandTransport{Command: cmd, TerminateDuration: 2 * time.Second}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
		return nil, &StartError{Command: command, Err: err, Stderr: stderr.String()}
	}
	s := &Server{Workspace: abs, session: session, stderr: stderr}
	init := session.InitializeResult()
	if init != nil {
		s.Instructions = init.Instructions
		if init.ServerInfo != nil {
			s.Info = ServerInfo{Name: init.ServerInfo.Name, Version: init.ServerInfo.Version}
		}
	}
	for t, err := range session.Tools(ctx, nil) {
		if err != nil {
			_ = s.Close()
			return nil, &StartError{Command: command, Err: fmt.Errorf("tools/list: %w", err), Stderr: stderr.String()}
		}
		s.Tools = append(s.Tools, t)
	}
	return s, nil
}

// Call forwards a tool call and returns its result as the text the model
// receives: text content verbatim, any other content as its JSON.
func (s *Server) Call(ctx context.Context, name string, args json.RawMessage) (text string, isError bool, err error) {
	var arguments any = map[string]any{}
	if len(args) > 0 {
		arguments = args
	}
	res, err := s.session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		return "", true, err
	}
	var parts []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, tc.Text)
			continue
		}
		b, err := json.Marshal(c)
		if err != nil {
			return "", true, err
		}
		parts = append(parts, string(b))
	}
	if len(parts) == 0 && res.StructuredContent != nil {
		b, err := json.Marshal(res.StructuredContent)
		if err != nil {
			return "", true, err
		}
		parts = append(parts, string(b))
	}
	return strings.Join(parts, "\n"), res.IsError, nil
}

// Stderr returns what the server has written to stderr so far.
func (s *Server) Stderr() string { return s.stderr.String() }

// Close ends the session and the server process.
func (s *Server) Close() error { return s.session.Close() }

// Version is the harness version reported as the MCP client.
var Version = "dev"

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// syncBuffer is a bytes.Buffer safe for the process's stderr goroutine,
// keeping only the last 64 KiB.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n, err := b.buf.Write(p)
	if over := b.buf.Len() - 64<<10; over > 0 {
		b.buf.Next(over)
	}
	return n, err
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
