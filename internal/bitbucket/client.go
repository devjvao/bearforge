// Package bitbucket implements read-only Cloud and Server/Data Center PR discovery.
package bitbucket

// Client resolves Bitbucket coordinates and reads pull-request metadata.
// Construct clients with NewClient and reuse them across lookups.
type Client struct{}

// NewClient constructs a Bitbucket provider with process-local state.
func NewClient() *Client { return &Client{} }
