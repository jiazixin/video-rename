// Package app assembles the HTTP service. Domain logic lives in its owning package.
//
// Lifecycle: New opens the stores and recovers interrupted work without
// starting goroutines. Serve binds the listener, then calls Start to launch the
// background workers, and calls Shutdown after the HTTP server has drained.
// Work started by requests (marks, numbering, refreshes, manual scans) is
// detached from its request and cancelled by Shutdown.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"video-rename/internal/auth"
	"video-rename/internal/config"
	jobqueue "video-rename/internal/job"
	"video-rename/internal/library"
	"video-rename/internal/libraryscan"
	"video-rename/internal/notification"
	"video-rename/internal/scheduler"
	"video-rename/internal/storage"
	"video-rename/internal/systemsetting"
	"video-rename/internal/timefmt"
)

// App is the video-rename service with its stores, managers and background
// workers. markMu guards markJobs and markClosed; numberingMu guards
// numberingJobs and numberingClosed.
type App struct {
	logger          *slog.Logger
	build           Build
	requestMu       sync.Mutex
	requests        map[string]chan struct{}
	Notifications   *notification.Service
	db              *storage.Store
	Library         *library.Library
	Scanner         *libraryscan.Manager
	Jobs            *jobqueue.Manager
	Scheduler       *scheduler.Scheduler
	Auth            *auth.Auth
	stop            context.CancelFunc
	closeStores     func() error
	wg              sync.WaitGroup
	settingsMu      sync.Mutex
	markMu          sync.Mutex
	markJobs        map[string]*markJob
	markWG          sync.WaitGroup
	markClosed      bool
	numberingMu     sync.Mutex
	numberingJobs   map[string]*numberingJob
	numberingWG     sync.WaitGroup
	numberingClosed bool
}

// New opens the database and builds every component of the service from cfg.
func New(ctx context.Context, cfg *config.Config, logger *slog.Logger) (*App, error) {
	db, err := storage.Open(ctx, cfg.DatabasePath())
	if err != nil {
		return nil, err
	}
	l, err := library.New(ctx, db, cfg.Path, logger)
	if err != nil {
		return nil, errors.Join(err, db.Close())
	}
	j, err := jobqueue.New(ctx, jobqueue.Options{Store: db, Library: l, Config: cfg, Logger: logger})
	if err != nil {
		return nil, errors.Join(err, db.Close())
	}
	s := scheduler.New(db, j)
	if err = s.ResetNext(ctx, time.Now()); err != nil {
		return nil, errors.Join(err, j.Cache.Close(), db.Close())
	}
	administrator, err := auth.New(ctx, db, cfg.Auth)
	if err != nil {
		return nil, errors.Join(err, j.Cache.Close(), db.Close())
	}
	return &App{logger: logger, Notifications: notification.New(db, logger), db: db, Library: l, Scanner: libraryscan.New(l, systemsetting.New(db)), Jobs: j, Scheduler: s, Auth: administrator,
		closeStores: sync.OnceValue(func() error { return errors.Join(j.Cache.Close(), db.Close()) })}, nil
}

var errStarted = errors.New("app is already started")

// Start launches the background workers under ctx; Shutdown stops them.
func (a *App) Start(ctx context.Context) error {
	if a.stop != nil {
		return errStarted
	}
	ctx, a.stop = context.WithCancel(ctx)
	workers := []struct {
		name string
		run  func(context.Context) error
	}{
		{"notifications", a.Notifications.Run},
		{"jobs", a.Jobs.Run},
		{"scheduler", a.Scheduler.Run},
		{"library_scan", a.Scanner.Run},
	}
	for _, w := range workers {
		a.wg.Go(func() {
			if err := w.run(ctx); err != nil {
				a.logger.Error("worker_failed", "worker", w.name, "error", err)
			}
		})
	}
	return nil
}

// Shutdown cancels request-started work and the background workers, waits for
// them and closes every component; later calls only repeat the result. If ctx
// ends first it returns its cause and leaves the stores open for the work that
// is still stopping.
func (a *App) Shutdown(ctx context.Context) error {
	a.markMu.Lock()
	a.markClosed = true
	for _, job := range a.markJobs {
		if job.cancel != nil {
			job.cancel()
		}
	}
	a.markMu.Unlock()
	a.numberingMu.Lock()
	a.numberingClosed = true
	for _, job := range a.numberingJobs {
		if job.cancel != nil {
			job.cancel()
		}
	}
	a.numberingMu.Unlock()
	if a.stop != nil {
		a.stop()
	}
	stopped := make(chan struct{})
	var stopping sync.WaitGroup
	// The waiter ends with the owned work even if this call reaches its deadline.
	// Do not defer Wait: a timeout must leave the stores open and return promptly.
	stopping.Go(func() {
		defer close(stopped)
		a.Scanner.Close()
		a.numberingWG.Wait()
		a.markWG.Wait()
		a.wg.Wait()
		a.Library.Close()
	})
	select {
	case <-stopped:
	case <-ctx.Done():
		return fmt.Errorf("wait for background work: %w", context.Cause(ctx))
	}
	stopping.Wait()
	return a.closeStores()
}

const (
	// shutdownTimeout bounds graceful HTTP shutdown before connections are closed.
	shutdownTimeout = 10 * time.Second
	// workerStopTimeout bounds waiting for background work after HTTP shutdown.
	workerStopTimeout = 30 * time.Second
)

// Serve listens before starting background work and shuts down when ctx ends.
func Serve(ctx context.Context, options ServeOptions) (err error) {
	cfg, logger := options.Config, options.Logger
	a, err := New(ctx, &cfg, logger)
	if err != nil {
		return err
	}
	a.build = options.Build
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), workerStopTimeout)
		defer cancel()
		if stopErr := a.Shutdown(stopCtx); stopErr != nil {
			err = errors.Join(err, fmt.Errorf("shut down app: %w", stopErr))
		}
	}()
	srv := &http.Server{
		Addr:              net.JoinHostPort(cfg.Listen, strconv.Itoa(cfg.Port)),
		Handler:           a.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      time.Minute,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}
	listener, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return fmt.Errorf("http listen failed: %w", err)
	}
	var serving sync.WaitGroup
	defer func() {
		// Serve and Shutdown usually close the listener before this cleanup.
		if closeErr := listener.Close(); closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
			err = errors.Join(err, fmt.Errorf("close http listener: %w", closeErr))
		}
		serving.Wait()
	}()
	// Workers keep running while HTTP drains; the deferred Shutdown stops them.
	if err := a.Start(context.WithoutCancel(ctx)); err != nil {
		return err
	}
	logger.Info("service_listening", "listen", listener.Addr().String())
	done := make(chan error, 1)
	serving.Go(func() { done <- srv.Serve(listener) })
	select {
	case err := <-done:
		return fmt.Errorf("serve http: %w", err)
	case <-ctx.Done():
	}
	logger.Info("service_stopping")
	shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil {
		return errors.Join(fmt.Errorf("shut down http server: %w", err), srv.Close())
	}
	if err := <-done; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve http: %w", err)
	}
	return nil
}

// Build contains the binary identity determined by the command entrypoint.
type Build struct {
	Version  string       `json:"version"`
	Revision string       `json:"revision,omitzero"`
	Time     timefmt.Time `json:"build_time,omitzero"`
	Modified bool         `json:"modified"`
}

// ServeOptions supplies the assembled server inputs.
type ServeOptions struct {
	Config config.Config
	Logger *slog.Logger
	Build  Build
}
