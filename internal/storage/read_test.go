package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestReadPoolIsolationAndWriterAvailability(t *testing.T) {
	s, err := Open(t.Context(), filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.Exec("INSERT INTO prompts(id,title,body,created,updated) VALUES(1,'before','body','now','now')"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	write, err := s.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer write.Rollback()
	if _, err = write.Exec("UPDATE prompts SET title='after' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.Read().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Rollback()
	var title string
	if err = snapshot.QueryRowContext(ctx, "SELECT title FROM prompts WHERE id=1").Scan(&title); err != nil || title != "before" {
		t.Fatalf("read during write: %q %v", title, err)
	}
	if err = write.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ExecContext(ctx, "UPDATE prompts SET body='changed' WHERE id=1"); err != nil {
		t.Fatalf("reader blocked writer: %v", err)
	}
	if err = snapshot.QueryRowContext(ctx, "SELECT title FROM prompts WHERE id=1").Scan(&title); err != nil || title != "before" {
		t.Fatalf("unstable snapshot: %q %v", title, err)
	}
	if err = snapshot.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = s.Read().QueryRowContext(ctx, "SELECT title FROM prompts WHERE id=1").Scan(&title); err != nil || title != "after" {
		t.Fatalf("committed write invisible: %q %v", title, err)
	}
	// Hold every reader so settings are checked on distinct physical connections.
	conns := []*sql.Conn{}
	defer func() {
		for _, c := range conns {
			c.Close()
		}
	}()
	for range 4 {
		c, err := s.Read().Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
		var enabled int
		if err = c.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&enabled); err != nil || enabled != 1 {
			t.Fatalf("foreign keys: %d %v", enabled, err)
		}
		if _, err = c.ExecContext(ctx, "UPDATE prompts SET title='forbidden'"); err == nil {
			t.Fatal("reader accepted write")
		}
		if err = c.QueryRowContext(ctx, "SELECT 'file2' COLLATE NATURAL_NAME < 'file10'").Scan(&enabled); err != nil || enabled != 1 {
			t.Fatalf("reader collation: %d %v", enabled, err)
		}
	}
	if _, err = s.ExecContext(ctx, "UPDATE prompts SET body='writer remains available' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
}
