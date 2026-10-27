// Package app assembles the HTTP service. Domain logic lives in its owning package.
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
	"video-rename/internal/jobs"
	"video-rename/internal/library"
	"video-rename/internal/libraryscan"
	"video-rename/internal/notifications"
	"video-rename/internal/scheduler"
	"video-rename/internal/storage"
	"video-rename/internal/systemsettings"
)

// App is the video-rename service with its stores, managers and background
// workers.
type App struct {
	logger          *slog.Logger
	version         string
	Notifications   *notifications.Service
	DB              *storage.Store
	Library         *library.Library
	Scanner         *libraryscan.Manager
	Jobs            *jobs.Manager
	Scheduler       *scheduler.Scheduler
	Auth            *auth.Auth
	ctx             context.Context
	cancel          context.CancelFunc
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
func New(cfg *config.Config, logger *slog.Logger) (*App, error) {
	db, err := storage.Open(cfg.DatabasePath())
	if err != nil {
		return nil, err
	}
	l, err := library.New(db, cfg.Path)
	if err != nil {
		db.Close()
		return nil, err
	}
	j, err := jobs.New(db, l, cfg, logger)
	if err != nil {
		db.Close()
		return nil, err
	}
	s := &scheduler.Scheduler{DB: db, Jobs: j}
	if err = s.ResetNext(time.Now()); err != nil {
		j.Cache.Close()
		db.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	l.RefreshContext = ctx
	return &App{logger: logger, Notifications: notifications.New(db, logger), DB: db, Library: l, Scanner: libraryscan.New(l, systemsettings.Store{DB: db}), Jobs: j, Scheduler: s, Auth: auth.New(db, cfg.Auth), ctx: ctx, cancel: cancel}, nil
}

// Start starts the background workers.
func (a *App) Start() {
	a.wg.Go(func() { a.Notifications.Loop(a.ctx) })
	a.wg.Go(func() { a.Jobs.Loop(a.ctx) })
	a.wg.Go(func() { a.Scheduler.Loop(a.ctx) })
	a.wg.Go(func() { a.Scanner.Loop(a.ctx) })
}

// Close stops the background workers and closes every component.
func (a *App) Close() error {
	a.markMu.Lock()
	a.markClosed = true
	a.markMu.Unlock()
	a.numberingMu.Lock()
	a.numberingClosed = true
	a.numberingMu.Unlock()
	a.cancel()
	a.Scanner.Close()
	a.numberingWG.Wait()
	a.markWG.Wait()
	a.wg.Wait()
	a.Library.Close()
	a.Jobs.Cache.Close()
	return a.DB.Close()
}

// shutdownTimeout bounds graceful HTTP shutdown before connections are closed.
const shutdownTimeout = 10 * time.Second

// Serve listens before starting background work and shuts down when ctx ends.
func Serve(ctx context.Context, cfg config.Config, logger *slog.Logger, version string) (err error) {
	a, err := New(&cfg, logger)
	if err != nil {
		return err
	}
	a.version = version
	defer func() {
		if closeErr := a.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close app: %w", closeErr))
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
	defer listener.Close()
	a.Start()
	logger.Info("service_listening", "listen", listener.Addr().String())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(listener) }()
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
