// A minimal company-owned MCP adapter. Demo data only; put this HTTP listener
// behind a reachable HTTPS reverse proxy before connecting it to Nudgy.
package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ticketInput struct {
	ID string `json:"id" jsonschema:"Ticket ID, for example DEMO-42"`
}
type ticketOutput struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

func main() {
	token := os.Getenv("COMPANY_MCP_TOKEN")
	if len(token) < 32 {
		log.Fatal("set COMPANY_MCP_TOKEN to a dedicated random token of at least 32 characters")
	}
	expected := sha256.Sum256([]byte("Bearer " + token))
	server := mcp.NewServer(&mcp.Implementation{Name: "company-tickets-demo", Version: "1.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "ticket_read", Description: "Read a synthetic company ticket. Demo ticket: DEMO-42.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, func(_ context.Context, _ *mcp.CallToolRequest, input ticketInput) (*mcp.CallToolResult, ticketOutput, error) {
		if input.ID != "DEMO-42" {
			return nil, ticketOutput{}, errors.New("ticket unavailable")
		}
		return nil, ticketOutput{ID: "DEMO-42", Title: "Connect the company application to Nudgy", Status: "open"}, nil
	})
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	mux := http.NewServeMux()
	mux.Handle("/mcp", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actual := sha256.Sum256([]byte(r.Header.Get("Authorization")))
		if subtle.ConstantTimeCompare(actual[:], expected[:]) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		// This endpoint is server-to-server. Browser-origin requests are not needed.
		if r.Header.Get("Origin") != "" {
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 65536)
		mcpHandler.ServeHTTP(w, r)
	}))
	httpServer := &http.Server{Addr: "127.0.0.1:8090", Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second}
	log.Print("Demo MCP listening on " + strings.TrimPrefix(httpServer.Addr, "http://") + "/mcp; publish through an HTTPS reverse proxy")
	log.Fatal(httpServer.ListenAndServe())
}
