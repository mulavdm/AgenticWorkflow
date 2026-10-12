package ipc

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"

	"gopkg.in/natefinch/npipe.v2"
)

// Client handles IPC communication with the Rust sidecar over Named Pipes
type Client struct {
	pipePath string
	conn     net.Conn
}

func NewClient(pipePath string) *Client {
	return &Client{pipePath: pipePath}
}

// Connect attempts to connect to the named pipe with retries
func (c *Client) Connect(ctx context.Context, maxRetries int) error {
	var err error
	for i := 0; i < maxRetries; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		c.conn, err = npipe.DialTimeout(c.pipePath, 2*time.Second)
		if err == nil {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("failed to connect to IPC named pipe %s after %d retries: %w", c.pipePath, maxRetries, err)
}

func (c *Client) Close() {
	if c.conn != nil {
		c.conn.Close()
	}
}

// SendMessage sends a raw flatbuffers message and returns the raw response payload
func (c *Client) SendMessage(buf []byte) ([]byte, error) {
	if c.conn == nil {
		return nil, fmt.Errorf("IPC client not connected")
	}

	// Send length prefix (4 bytes)
	lenBuf := make([]byte, 4)
	binary.LittleEndian.PutUint32(lenBuf, uint32(len(buf)))
	if _, err := c.conn.Write(lenBuf); err != nil {
		return nil, fmt.Errorf("failed to write length prefix: %w", err)
	}

	// Send message
	if _, err := c.conn.Write(buf); err != nil {
		return nil, fmt.Errorf("failed to write payload: %w", err)
	}

	// Read response length
	if _, err := io.ReadFull(c.conn, lenBuf); err != nil {
		return nil, fmt.Errorf("failed to read response length: %w", err)
	}
	respLen := binary.LittleEndian.Uint32(lenBuf)

	// Read response payload
	respBuf := make([]byte, respLen)
	if _, err := io.ReadFull(c.conn, respBuf); err != nil {
		return nil, fmt.Errorf("failed to read response payload: %w", err)
	}

	return respBuf, nil
}
