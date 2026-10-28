// Package prompt owns the reusable prompt library referenced by model configs.
package prompt

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"

	"video-rename/internal/domain"
	"video-rename/internal/storage"
)

// Profile is a reusable prompt.
type Profile struct {
	ID      int64  `json:"id"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	Version int64  `json:"version"`
	Created string `json:"created"`
	Updated string `json:"updated"`
}

// Store stores prompts.
type Store struct{ db *storage.Store }

const columns = "id,title,body,version,created,updated"

// Get returns prompt id.
func (s Store) Get(ctx context.Context, id int64) (Profile, error) {
	var p Profile
	err := s.db.View(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT "+columns+" FROM prompts WHERE id=?", id).Scan(&p.ID, &p.Title, &p.Body, &p.Version, &p.Created, &p.Updated)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return p, domain.Fail(domain.KindNotFound, domain.Record{Code: "prompt_not_found", Message: "prompt does not exist"})
	}
	return p, err
}

// List returns all prompts.
func (s Store) List(ctx context.Context) ([]Profile, error) {
	return storage.ReadValue(ctx, s.db, func(tx *sql.Tx) ([]Profile, error) {
		rows, err := tx.QueryContext(ctx, "SELECT "+columns+" FROM prompts ORDER BY id")
		if err != nil {
			return nil, err
		}
		return storage.CollectRows(rows, func(rows *sql.Rows) (Profile, error) {
			var p Profile
			err := rows.Scan(&p.ID, &p.Title, &p.Body, &p.Version, &p.Created, &p.Updated)
			return p, err
		})
	})
}

func (p *Profile) validate() error {
	p.Title = strings.TrimSpace(p.Title)
	p.Body = strings.TrimSpace(p.Body)
	if p.Title == "" || utf8.RuneCountInString(p.Title) > 80 {
		return domain.InvalidField("title", "invalid_length")
	}
	if p.Body == "" || len(p.Body) > 16000 {
		return domain.InvalidField("body", "invalid_length")
	}
	return nil
}

// Save creates or updates prompt p.
func (s Store) Save(ctx context.Context, p Profile) (Profile, error) {
	if err := p.validate(); err != nil {
		return p, err
	}
	var err error
	if p.ID == 0 {
		p, err = storage.WriteValue(ctx, s.db, func(tx *sql.Tx) (Profile, error) {
			var n int
			if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM prompts").Scan(&n); err != nil {
				return p, err
			}
			if n >= 200 {
				return p, domain.Fail(domain.KindTooMany, domain.Record{Code: "prompts_full", Message: "at most 200 prompts"})
			}
			res, err := tx.ExecContext(ctx, "INSERT INTO prompts(title,body,created,updated) VALUES(?,?,?,?)", p.Title, p.Body, storage.Now(), storage.Now())
			if err != nil {
				return p, err
			}
			p.ID, err = res.LastInsertId()
			if err != nil {
				return p, err
			}
			return p, nil
		})
		if err != nil {
			return p, err
		}
	} else {
		res, err := storage.WriteValue(ctx, s.db, func(tx *sql.Tx) (sql.Result, error) {
			return tx.ExecContext(ctx, "UPDATE prompts SET title=?,body=?,updated=?,version=version+1 WHERE id=? AND version=?", p.Title, p.Body, storage.Now(), p.ID, p.Version)
		})
		if err != nil {
			return p, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return p, err
		}
		if n == 0 {
			return p, domain.Fail(domain.KindConflict, domain.Record{Code: "prompt_changed", Message: "prompt was modified or deleted; refresh"})
		}
	}
	return s.Get(ctx, p.ID)
}

// Delete deletes prompt id if its version is still version.
func (s Store) Delete(ctx context.Context, id, version int64) error {
	return s.db.Write(ctx, func(tx *sql.Tx) error {
		var err error
		var current int64
		if err = tx.QueryRowContext(ctx, "SELECT version FROM prompts WHERE id=?", id).Scan(&current); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return domain.Fail(domain.KindNotFound, domain.Record{Code: "prompt_not_found", Message: "prompt does not exist"})
			}
			return err
		}
		if current != version {
			return domain.Fail(domain.KindConflict, domain.Record{Code: "prompt_changed", Message: "prompt was modified; refresh"})
		}
		var configs int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM model_configs WHERE prompt_id=?", id).Scan(&configs); err != nil {
			return err
		}
		if configs > 0 {
			return domain.Fail(domain.KindConflict, domain.Record{Code: "prompt_in_use", Message: "prompt is used by model configurations", Details: map[string]any{"configs": configs}})
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM prompts WHERE id=?", id); err != nil {
			return err
		}
		return nil
	})
}

// New creates the prompts store.
func New(db *storage.Store) Store { return Store{db: db} }
