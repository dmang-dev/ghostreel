// SPDX-License-Identifier: Apache-2.0

// Package engine drives a real SC2 client through Blizzard's s2client API to
// play a replay back and record the exact state of every unit. It is the
// engine tier: ground truth for measuring the reconstructed tiers, and exact
// keyframes for timelines when a matching client is available.
//
// Only cmd/ghostreel-engine uses this package; the viewer and core never
// need a game install.
package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/coder/websocket"
	"google.golang.org/protobuf/proto"

	sc2 "github.com/OWNER/ghostreel/internal/engine/s2api/s2clientprotocol"
)

// Client is a connection to one SC2 instance's API endpoint.
type Client struct {
	conn *websocket.Conn
}

// Dial connects to ws://addr/sc2api, retrying until the client is up or ctx
// ends (SC2 takes a few seconds to start listening).
func Dial(ctx context.Context, addr string) (*Client, error) {
	url := "ws://" + addr + "/sc2api"
	var lastErr error
	for {
		dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		conn, _, err := websocket.Dial(dctx, url, nil)
		cancel()
		if err == nil {
			// Observations of big games exceed the default 32 KiB read limit.
			conn.SetReadLimit(256 << 20)
			return &Client{conn: conn}, nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("connecting to SC2 at %s: %w (last error: %v)", url, ctx.Err(), lastErr)
		case <-time.After(time.Second):
		}
	}
}

// Close closes the connection (not the game).
func (c *Client) Close() error { return c.conn.Close(websocket.StatusNormalClosure, "") }

// Call sends one request and waits for its response. API-level errors in the
// response are returned as an error.
func (c *Client) Call(ctx context.Context, req *sc2.Request) (*sc2.Response, error) {
	data, err := proto.Marshal(req)
	if err != nil {
		return nil, err
	}
	if err := c.conn.Write(ctx, websocket.MessageBinary, data); err != nil {
		return nil, err
	}
	_, msg, err := c.conn.Read(ctx)
	if err != nil {
		return nil, err
	}
	resp := &sc2.Response{}
	if err := proto.Unmarshal(msg, resp); err != nil {
		return nil, err
	}
	if len(resp.Error) > 0 {
		return resp, errors.New("sc2: " + strings.Join(resp.Error, "; "))
	}
	return resp, nil
}
