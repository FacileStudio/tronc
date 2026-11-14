// Package joblock lets one replica of an app run a background job at a time.
//
// Every replica calls [Hold] with the same key. One takes a Postgres session
// advisory lock on a dedicated connection and runs the job; the others retry
// until the holder stops or its connection drops.
package joblock

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"time"
)

const (
	DefaultRetry = 5 * time.Second
	DefaultCheck = 5 * time.Second

	keyPrefix = "joblock:"
	tryLock   = `SELECT pg_try_advisory_lock(hashtextextended($1, 0))`
	unlock    = `SELECT pg_advisory_unlock(hashtextextended($1, 0))`
	ping      = `SELECT 1`
)

// Option tunes [Hold].
type Option func(*config)

type config struct {
	retry   time.Duration
	check   time.Duration
	onError func(error)
}

// WithRetry sets how long a replica that does not hold the lock waits before
// trying again. The default is [DefaultRetry].
func WithRetry(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.retry = d
		}
	}
}

// WithCheck sets how often the holder probes its connection. A failed probe
// cancels the job and gives the lock up. The default is [DefaultCheck].
func WithCheck(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.check = d
		}
	}
}

// WithOnError reports a failed lock attempt or a lost connection. It is called
// from the goroutine running [Hold].
func WithOnError(f func(error)) Option {
	return func(c *config) {
		if f != nil {
			c.onError = f
		}
	}
}

// Hold blocks until ctx ends. Whenever this replica holds the lock named key
// it calls run with a context that is cancelled when the lock is lost or ctx
// ends. run must return soon after its context is cancelled: the lock is only
// released after run returns, so two replicas never run the job at once.
//
// If the connection holding the lock drops, run's context is cancelled, and
// Hold goes back to waiting for the lock. If run returns while the lock is
// still held, Hold releases it and returns nil. Hold also returns nil when ctx
// ends, and an error only for an invalid argument.
//
// The lock pins one connection of db for as long as it is held, so db must
// allow more than one open connection.
func Hold(ctx context.Context, db *sql.DB, key string, run func(context.Context), opts ...Option) error {
	if db == nil {
		return errors.New("joblock: nil database")
	}
	if key == "" {
		return errors.New("joblock: empty key")
	}
	if run == nil {
		return errors.New("joblock: nil run function")
	}
	c := config{retry: DefaultRetry, check: DefaultCheck, onError: func(error) {}}
	for _, opt := range opts {
		opt(&c)
	}
	name := keyPrefix + key
	for {
		conn := acquire(ctx, db, name, c)
		if conn == nil {
			return nil
		}
		if hold(ctx, conn, name, run, c) {
			return nil
		}
		if ctx.Err() != nil {
			return nil
		}
	}
}

func acquire(ctx context.Context, db *sql.DB, name string, c config) *sql.Conn {
	for {
		if conn, err := tryAcquire(ctx, db, name); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			c.onError(err)
		} else if conn != nil {
			return conn
		}
		if !sleep(ctx, c.retry) {
			return nil
		}
	}
}

func tryAcquire(ctx context.Context, db *sql.DB, name string) (*sql.Conn, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	var got bool
	if err := conn.QueryRowContext(ctx, tryLock, name).Scan(&got); err != nil {
		discard(conn)
		return nil, err
	}
	if !got {
		_ = conn.Close()
		return nil, nil
	}
	return conn, nil
}

func hold(ctx context.Context, conn *sql.Conn, name string, run func(context.Context), c config) bool {
	heldCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		run(heldCtx)
	}()
	ticker := time.NewTicker(c.check)
	defer ticker.Stop()
	finished := false
loop:
	for {
		select {
		case <-done:
			finished = true
			break loop
		case <-ctx.Done():
			break loop
		case <-ticker.C:
			if err := probe(ctx, conn, c.check); err != nil && ctx.Err() == nil {
				c.onError(err)
				break loop
			}
		}
	}
	cancel()
	<-done
	release(conn, name)
	return finished
}

func probe(ctx context.Context, conn *sql.Conn, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	_, err := conn.ExecContext(ctx, ping)
	return err
}

func release(conn *sql.Conn, name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _ = conn.ExecContext(ctx, unlock, name)
	discard(conn)
}

func discard(conn *sql.Conn) {
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	_ = conn.Close()
}

func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
