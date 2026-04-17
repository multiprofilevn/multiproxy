package multiproxy

import (
	"context"
	"io"
	"net"
	"time"

	"golang.org/x/sync/errgroup"
)

const (
	IdleTimeout = 60 * time.Second
	CopyBufSize = 32 * 1024
)

// copyWithDeadline copies src→dst, refreshing deadline on each read.
// Returns nil on clean EOF, or the first non-EOF error encountered.
func copyWithDeadline(dst net.Conn, src net.Conn, idleTimeout time.Duration) error {
	buf := make([]byte, CopyBufSize)
	for {
		src.SetReadDeadline(time.Now().Add(idleTimeout))
		n, readErr := src.Read(buf)
		if n > 0 {
			dst.SetWriteDeadline(time.Now().Add(idleTimeout))
			if _, writeErr := dst.Write(buf[:n]); writeErr != nil {
				return writeErr
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				return nil
			}
			return readErr
		}
	}
}

// PairConnections copies bidirectionally between incoming and outgoing connections.
// It uses errgroup to manage both copy goroutines. When the context is cancelled
// or either direction finishes, both connections are closed so the other goroutine
// unblocks. Returns the first non-nil error from either direction or the context.
func PairConnections(ctx context.Context, incoming, outgoing net.Conn) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Close connections when context is done so blocked reads unblock.
	go func() {
		<-ctx.Done()
		incoming.Close()
		outgoing.Close()
	}()

	eg, _ := errgroup.WithContext(ctx)

	eg.Go(func() error {
		err := copyWithDeadline(outgoing, incoming, IdleTimeout)
		cancel() // signal the other direction to stop
		return err
	})

	eg.Go(func() error {
		err := copyWithDeadline(incoming, outgoing, IdleTimeout)
		cancel()
		return err
	})

	return eg.Wait()
}
