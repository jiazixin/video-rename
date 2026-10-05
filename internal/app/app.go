// Package app assembles the HTTP service. Domain logic lives in its owning package.
package app

import (
	"context"
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

type App struct {
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

func New(cfg *config.Config) (*App, error) {
	db, err := storage.Open(cfg.DatabasePath())
	if err != nil {
		return nil, err
	}
	l, err := library.New(db, cfg.Path)
	if err != nil {
		db.Close()
		return nil, err
	}
	j, err := jobs.New(db, l, cfg)
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
	return &App{Notifications: notifications.New(db), DB: db, Library: l, Scanner: libraryscan.New(l, systemsettings.Store{DB: db}), Jobs: j, Scheduler: s, Auth: auth.New(db, cfg.Auth), ctx: ctx, cancel: cancel}, nil
}
func (a *App) Start() {
	a.wg.Go(func() { a.Notifications.Loop(a.ctx) })
	a.wg.Go(func() { a.Jobs.Loop(a.ctx) })
	a.wg.Go(func() { a.Scheduler.Loop(a.ctx) })
	a.wg.Go(func() { a.Scanner.Loop(a.ctx) })
}
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
func Serve(ctx context.Context, cfg *config.Config) error {
	a, err := New(cfg)
	if err != nil {
		return err
	}
	defer a.Close()
	srv := &http.Server{Addr: net.JoinHostPort(cfg.Listen, strconv.Itoa(cfg.Port)), Handler: a.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	listener, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return fmt.Errorf("http listen failed: %w", err)
	}
	defer listener.Close()
	a.Start()
	slog.Info("http server started", "listen", cfg.Listen, "port", cfg.Port)
	slog.Info("scheduler started")
	done := make(chan error, 1)
	go func() { done <- srv.Serve(listener) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdown)
	}
}
