// Package prompts owns the reusable prompt library referenced by model configs.
package prompts

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
	"video-rename/internal/storage"
)

type Profile struct {
	ID      int64  `json:"id"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	Version int64  `json:"version"`
	Created string `json:"created"`
	Updated string `json:"updated"`
}
type Store struct{ DB *storage.Store }

const columns = "id,title,body,version,created,updated"

func (s Store) Get(id int64) (Profile, error) {
	var p Profile
	err := s.DB.QueryRow("SELECT "+columns+" FROM prompts WHERE id=?", id).Scan(&p.ID, &p.Title, &p.Body, &p.Version, &p.Created, &p.Updated)
	if errors.Is(err, sql.ErrNoRows) {
		return p, fmt.Errorf("提示词不存在")
	}
	return p, err
}
func (s Store) List() ([]Profile, error) {
	rows, err := s.DB.Query("SELECT " + columns + " FROM prompts ORDER BY id")
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
	if p.Title == "" || utf8.RuneCountInString(p.Title) > 80 || p.Body == "" || len(p.Body) > 16000 {
		return fmt.Errorf("名称须为 1–80 字，提示词内容不能为空且不得超过 16000 字节")
	}
	return nil
}
func (s Store) Save(p Profile) (Profile, error) {
	if err := p.validate(); err != nil {
		return p, err
	}
	if p.ID == 0 {
		tx, err := s.DB.Begin()
		if err != nil {
			return p, err
		}
		defer tx.Rollback()
		var n int
		if err = tx.QueryRow("SELECT count(*) FROM prompts").Scan(&n); err != nil {
			return p, err
		}
		if n >= 200 {
			return p, fmt.Errorf("最多保存 200 条提示词")
		}
		res, err := tx.Exec("INSERT INTO prompts(title,body,created,updated) VALUES(?,?,?,?)", p.Title, p.Body, storage.Now(), storage.Now())
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
		res, err := s.DB.Exec("UPDATE prompts SET title=?,body=?,updated=?,version=version+1 WHERE id=? AND version=?", p.Title, p.Body, storage.Now(), p.ID, p.Version)
		if err != nil {
			return p, err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return p, fmt.Errorf("提示词已修改或不存在，请刷新后重试")
		}
	}
	return s.Get(p.ID)
}
func (s Store) Delete(id, version int64) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current int64
	if err = tx.QueryRow("SELECT version FROM prompts WHERE id=?", id).Scan(&current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("提示词不存在")
		}
		return err
	}
	if current != version {
		return fmt.Errorf("提示词已修改，请刷新后重试")
	}
	var configs int
	if err = tx.QueryRow("SELECT count(*) FROM model_configs WHERE prompt_id=?", id).Scan(&configs); err != nil {
		return err
	}
	if configs > 0 {
		return fmt.Errorf("有 %d 个模型配置使用此提示词，请先更换模型配置中的提示词", configs)
	}
	if _, err = tx.Exec("DELETE FROM prompts WHERE id=?", id); err != nil {
		return err
	}
	return tx.Commit()
}
