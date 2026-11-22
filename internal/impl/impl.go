// Package impl starts a DKF implementation from its command template and
// speaks MCP to it. The harness depends on nothing else an implementation
// provides.
//
// Starting, connecting and forwarding calls are agentloop's mcpclient. This
// package adds the DKF adapter contract: {workspace} and DKF_WORKSPACE are
// the workspace, which is also the working directory, and the harness names
// itself particulars-bench.
package impl

import (
	"context"
	"path/filepath"

	"github.com/nodelogicau/agentloop/mcpclient"
)

// Placeholder is replaced by the absolute workspace path in a command.
const Placeholder = "{workspace}"

// Harness is the MCP client name.
const Harness = "particulars-bench"

// Version is the harness version reported as the MCP client.
var Version = "dev"

// Impl is an implementation under test: a label and a command template.
type Impl = mcpclient.Impl

// ServerInfo identifies an implementation, from initialize.
type ServerInfo = mcpclient.ServerInfo

// StartError is a server that failed before initialize completed.
type StartError = mcpclient.StartError

// Server is a running implementation bound to one workspace.
type Server struct {
	*mcpclient.Server
	// Workspace is the absolute path substituted for {workspace}.
	Workspace string
}

// Parse reads "<label>=<command>"; the command must contain {workspace}.
func Parse(s string) (Impl, error) { return mcpclient.Parse(s, Placeholder) }

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
	s, err := mcpclient.Start(ctx, mcpclient.Command{
		Line: mcpclient.Expand(im.Command, Placeholder, abs),
		Dir:  abs,
		Env:  []string{"DKF_WORKSPACE=" + abs},
	}, mcpclient.Client{Name: Harness, Version: Version})
	if err != nil {
		return nil, err
	}
	return &Server{Server: s, Workspace: abs}, nil
}
