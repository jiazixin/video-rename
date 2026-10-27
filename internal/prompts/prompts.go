// Package prompts owns the reusable prompt library referenced by model configs.
package prompts

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
type Store struct{ DB *storage.Store }

const columns = "id,title,body,version,created,updated"

// Get returns prompt id.
func (s Store) Get(ctx context.Context, id int64) (Profile, error) {
	var p Profile
	err := s.DB.QueryRowContext(ctx, "SELECT "+columns+" FROM prompts WHERE id=?", id).Scan(&p.ID, &p.Title, &p.Body, &p.Version, &p.Created, &p.Updated)
	if errors.Is(err, sql.ErrNoRows) {
		return p, domain.Fail(domain.KindNotFound, "prompt_not_found", "prompt does not exist")
	}
	return p, err
}

// List returns all prompts.
func (s Store) List(ctx context.Context) ([]Profile, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+columns+" FROM prompts ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Profile{}
	for rows.Next() {
		var p Profile
		if err = rows.Scan(&p.ID, &p.Title, &p.Body, &p.Version, &p.Created, &p.Updated); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
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
	if p.ID == 0 {
		tx, err := s.DB.BeginTx(ctx, nil)
		if err != nil {
			return p, err
		}
		defer tx.Rollback()
		var n int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM prompts").Scan(&n); err != nil {
			return p, err
		}
		if n >= 200 {
			return p, domain.Fail(domain.KindTooMany, "prompts_full", "at most 200 prompts")
		}
		res, err := tx.ExecContext(ctx, "INSERT INTO prompts(title,body,created,updated) VALUES(?,?,?,?)", p.Title, p.Body, storage.Now(), storage.Now())
		if err != nil {
			return p, err
		}
		p.ID, err = res.LastInsertId()
		if err != nil {
			return p, err
		}
		if err = tx.Commit(); err != nil {
			return p, err
		}
	} else {
		res, err := s.DB.ExecContext(ctx, "UPDATE prompts SET title=?,body=?,updated=?,version=version+1 WHERE id=? AND version=?", p.Title, p.Body, storage.Now(), p.ID, p.Version)
		if err != nil {
			return p, err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return p, domain.Fail(domain.KindConflict, "prompt_changed", "prompt was modified or deleted; refresh")
		}
	}
	return s.Get(ctx, p.ID)
}

// Delete deletes prompt id if its version is still version.
func (s Store) Delete(ctx context.Context, id, version int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current int64
	if err = tx.QueryRowContext(ctx, "SELECT version FROM prompts WHERE id=?", id).Scan(&current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Fail(domain.KindNotFound, "prompt_not_found", "prompt does not exist")
		}
		return err
	}
	if current != version {
		return domain.Fail(domain.KindConflict, "prompt_changed", "prompt was modified; refresh")
	}
	var configs int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM model_configs WHERE prompt_id=?", id).Scan(&configs); err != nil {
		return err
	}
	if configs > 0 {
		return domain.Fail(domain.KindConflict, "prompt_in_use", "prompt is used by model configurations", "configs", configs)
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM prompts WHERE id=?", id); err != nil {
		return err
	}
	return tx.Commit()
}
