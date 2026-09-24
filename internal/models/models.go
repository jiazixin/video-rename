// Package models owns page-managed model profiles and their credentials.
package models

import (
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
	"video-rename/internal/config"
	"video-rename/internal/storage"
)

const DefaultBody = "你是一位视频标题编辑。结合整段视频的连续截图，为它创作一个自然、流畅、有辨识度的中文标题。抓住最有特点的情境或瞬间，将内容组织成完整表达，不要逐帧描述画面，也不要用名词、动作和场景堆砌关键词。可以有适度的表达创意，但不要编造人物身份、地点、关系或画面之外的事件。只输出一个标题，不加解释、引号或文件扩展名。"

type Profile struct {
	config.ServerConfig
	Default     bool   `json:"is_default"`
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	PromptID    int64  `json:"prompt_id"`
	PromptTitle string `json:"prompt_title"`
	Body        string `json:"body"`
	Version     int64  `json:"version"`
	Created     string `json:"created"`
	Updated     string `json:"updated"`
	HasAPIKey   bool   `json:"has_api_key"`
	Ready       bool   `json:"ready"`
}
type Store struct{ DB *storage.Store }

func DefaultSettings() config.ServerConfig {
	return config.ServerConfig{Workers: 2, Provider: "custom", Protocol: "openai", Timeout: "180s", Temperature: 0.7, MaxTokens: 1024}
}
func (s Store) Seed() error {
	if _, err := s.DB.Exec("INSERT INTO prompts(title,body,created,updated) SELECT '默认提示词',?,?,? WHERE NOT EXISTS(SELECT 1 FROM prompts)", DefaultBody, storage.Now(), storage.Now()); err != nil {
		return err
	}
	_, err := s.DB.Exec("INSERT INTO model_configs(id,title,prompt_id,created,updated,is_default) SELECT 1,'默认视频命名',(SELECT min(id) FROM prompts),?,?,1 WHERE NOT EXISTS(SELECT 1 FROM model_configs)", storage.Now(), storage.Now())
	return err
}

// columns resolves the referenced prompt via join: body and prompt_title are
// read-only views of the library entry, never stored on the config row.
const columns = "c.id,c.title,c.prompt_id,p.title,p.body,c.version,c.created,c.updated,c.is_default,c.settings,c.api_key"

func (s Store) scan(row interface{ Scan(...any) error }) (p Profile, err error) {
	var settings, secret string
	err = row.Scan(&p.ID, &p.Title, &p.PromptID, &p.PromptTitle, &p.Body, &p.Version, &p.Created, &p.Updated, &p.Default, &settings, &secret)
	if err != nil {
		return
	}
	p.ServerConfig = DefaultSettings()
	if err = json.Unmarshal([]byte(settings), &p.ServerConfig); err != nil {
		return
	}
	p.APIKey, err = s.DB.Unseal(secret)
	if err != nil {
		return
	}
	p.HasAPIKey = p.APIKey != ""
	p.Ready = p.Validate() == nil
	return
}
func (s Store) Get(id int64) (Profile, error) {
	return s.scan(s.DB.QueryRow("SELECT "+columns+" FROM model_configs c LEFT JOIN prompts p ON p.id=c.prompt_id WHERE c.id=?", id))
}
func (s Store) List() ([]Profile, error) {
	rows, err := s.DB.Query("SELECT " + columns + " FROM model_configs c LEFT JOIN prompts p ON p.id=c.prompt_id ORDER BY c.id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Profile{}
	for rows.Next() {
		p, err := s.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// resolve fills the read-only prompt fields from the library so validation
// and snapshots always see the text the reference currently points at.
func (s Store) resolve(p *Profile) error {
	if p.PromptID <= 0 {
		return fmt.Errorf("请选择提示词")
	}
	err := s.DB.QueryRow("SELECT title,body FROM prompts WHERE id=?", p.PromptID).Scan(&p.PromptTitle, &p.Body)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("请选择提示词")
	}
	return err
}
func (p *Profile) Validate() error {
	p.Title = strings.TrimSpace(p.Title)
	if p.Title == "" || utf8.RuneCountInString(p.Title) > 80 {
		return fmt.Errorf("名称须为 1–80 字")
	}
	if p.PromptID <= 0 || strings.TrimSpace(p.Body) == "" {
		return fmt.Errorf("请选择提示词")
	}
	var c config.Config
	if err := c.ApplyModel(p.ServerConfig, p.Body); err != nil {
		return err
	}
	p.ServerConfig = c.Server
	if p.Provider != "custom" && p.Provider != "ollama" && strings.TrimSpace(p.APIKey) == "" {
		return fmt.Errorf("请填写 API 密钥")
	}
	return nil
}
func (s Store) Save(p Profile) (Profile, error) {
	if err := s.resolve(&p); err != nil {
		return p, err
	}
	if err := p.Validate(); err != nil {
		return p, err
	}
	raw, err := json.Marshal(p.ServerConfig)
	if err != nil {
		return p, err
	}
	secret, err := s.DB.Seal(p.APIKey)
	if err != nil {
		return p, err
	}
	if p.ID == 0 {
		tx, err := s.DB.Begin()
		if err != nil {
			return p, err
		}
		defer tx.Rollback()
		var n int
		if err = tx.QueryRow("SELECT count(*) FROM model_configs").Scan(&n); err != nil {
			return p, err
		}
		if n >= 200 {
			return p, fmt.Errorf("最多保存 200 条模型配置")
		}
		res, err := tx.Exec("INSERT INTO model_configs(title,prompt_id,created,updated,settings,api_key) VALUES(?,?,?,?,?,?)", p.Title, p.PromptID, storage.Now(), storage.Now(), string(raw), secret)
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
		res, err := s.DB.Exec("UPDATE model_configs SET title=?,prompt_id=?,settings=?,api_key=?,updated=?,version=version+1 WHERE id=? AND version=?", p.Title, p.PromptID, string(raw), secret, storage.Now(), p.ID, p.Version)
		if err != nil {
			return p, err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return p, fmt.Errorf("模型配置已修改或不存在，请刷新后重试")
		}
	}
	return s.Get(p.ID)
}

// ResolveKey reuses a stored key only for the same destination and protocol.
func (s Store) ResolveKey(p *Profile, key *string) error {
	if key != nil {
		p.APIKey = *key
		return nil
	}
	if p.ID == 0 {
		return nil
	}
	old, err := s.Get(p.ID)
	if err != nil {
		return err
	}
	if old.BaseURL != p.BaseURL || old.Provider != p.Provider || old.Protocol != p.Protocol {
		if old.HasAPIKey {
			return fmt.Errorf("服务地址或类型已改变，请重新填写密钥，或明确清除旧密钥")
		}
		return nil
	}
	p.APIKey = old.APIKey
	return nil
}
func (s Store) SetDefault(id int64) error {
	p, err := s.Get(id)
	if err != nil {
		return err
	}
	if err = p.Validate(); err != nil {
		return err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE model_configs SET is_default=0 WHERE is_default=1"); err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE model_configs SET is_default=1 WHERE id=?", id); err != nil {
		return err
	}
	return tx.Commit()
}
func (s Store) DefaultID() (id int64, err error) {
	err = s.DB.QueryRow("SELECT id FROM model_configs WHERE is_default=1").Scan(&id)
	return
}

type snapshot struct {
	Settings config.ServerConfig
	Key      string
	Body     string
}

func (s Store) Snapshot(p Profile) (string, error) {
	raw, err := json.Marshal(snapshot{p.ServerConfig, p.APIKey, p.Body})
	if err != nil {
		return "", err
	}
	return s.DB.Seal(string(raw))
}
func (s Store) ApplySnapshot(value string, c *config.Config) error {
	if value == "" {
		return fmt.Errorf("任务没有模型配置快照，请重新创建任务")
	}
	plain, err := s.DB.Unseal(value)
	if err != nil {
		return err
	}
	var snap snapshot
	if err = json.Unmarshal([]byte(plain), &snap); err != nil {
		return fmt.Errorf("模型快照无效")
	}
	snap.Settings.APIKey = snap.Key
	return c.ApplyModel(snap.Settings, snap.Body)
}
