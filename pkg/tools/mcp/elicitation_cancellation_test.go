package mcp

import (
	"context"
	"testing"
	"time"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/docker/docker-agent/pkg/tools"
)

func TestMCPServerElicitationCanceledWithOwningToolCall(t *testing.T) {
	t.Parallel()
	server := gomcp.NewServer(&gomcp.Implementation{Name: "elicitation", Version: "1"}, nil)
	serverDone := make(chan error, 1)
	gomcp.AddTool(server, &gomcp.Tool{Name: "ask"}, func(_ context.Context, req *gomcp.CallToolRequest, _ struct{}) (*gomcp.CallToolResult, any, error) {
		// Deliberately do not cancel the nested request when tools/call is canceled.
		_, err := req.Session.Elicit(t.Context(), &gomcp.ElicitParams{Mode: "form", Message: "Choose", RequestedSchema: map[string]any{"type": "object", "properties": map[string]any{}}})
		serverDone <- err
		return &gomcp.CallToolResult{}, nil, err
	})
	clientTransport, serverTransport := gomcp.NewInMemoryTransports()
	ss, err := server.Connect(t.Context(), serverTransport, nil)
	require.NoError(t, err)
	defer func() { require.NoError(t, ss.Close()) }()
	client := &sessionClient{}
	entered, finished := make(chan struct{}), make(chan struct{})
	client.SetElicitationHandler(tools.ScopedElicitationHandler)
	sdkClient := gomcp.NewClient(&gomcp.Implementation{Name: "test", Version: "1"}, &gomcp.ClientOptions{ElicitationHandler: client.handleElicitationRequest})
	cs, err := sdkClient.Connect(t.Context(), clientTransport, &gomcp.ClientSessionOptions{ProtocolVersion: "2025-11-25"})
	require.NoError(t, err)
	client.setSession(cs)
	defer func() { require.NoError(t, cs.Close()) }()
	ctx, cancel := context.WithCancel(tools.WithHandlerScope(t.Context(), tools.HandlerScope{Elicitation: func(ctx context.Context, _ *gomcp.ElicitParams) (tools.ElicitationResult, error) {
		close(entered)
		<-ctx.Done()
		close(finished)
		return tools.ElicitationResult{}, ctx.Err()
	}}))
	defer cancel()
	called := make(chan error, 1)
	go func() { _, err := client.CallTool(ctx, &gomcp.CallToolParams{Name: "ask"}); called <- err }()
	select {
	case <-entered:
	case err := <-called:
		t.Fatalf("tool returned before elicitation: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("elicitation handler not entered")
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("nested elicitation outlived owning call")
	}
	require.Error(t, <-called)
	require.Error(t, <-serverDone)
}
