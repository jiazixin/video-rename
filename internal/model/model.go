// Package model owns page-managed model profiles and their credentials.
package model

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"strings"
	"unicode/utf8"

	"video-rename/internal/config"
	"video-rename/internal/domain"
	"video-rename/internal/secret"
	"video-rename/internal/storage"
)

// DefaultBody is the default prompt text.
const DefaultBody = "你是一位视频标题编辑。结合整段视频的连续截图，为它创作一个自然、流畅、有辨识度的中文标题。抓住最有特点的情境或瞬间，将内容组织成完整表达，不要逐帧描述画面，也不要用名词、动作和场景堆砌关键词。可以有适度的表达创意，但不要编造人物身份、地点、关系或画面之外的事件。只输出一个标题，不加解释、引号或文件扩展名。"

// Profile is a page-managed model configuration.
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

// Store stores model profiles.
type Store struct{ db *storage.Store }

// DefaultSettings returns the default model settings.
func DefaultSettings() config.ServerConfig {
	return config.ServerConfig{Workers: 2, Provider: "custom", Protocol: "openai", TimeoutMS: 180000, Temperature: 0.7, MaxTokens: 1024}
}

// Seed creates the default prompt and model profile when none exist.
func (s Store) Seed(ctx context.Context) error {
	if _, err := storage.WriteValue(ctx, s.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(ctx, "INSERT INTO prompts(title,body,created,updated) SELECT '默认提示词',?,?,? WHERE NOT EXISTS(SELECT 1 FROM prompts)", DefaultBody, storage.Now(), storage.Now())
	}); err != nil {
		return err
	}
	_, err := storage.WriteValue(ctx, s.db, func(tx *sql.Tx) (sql.Result, error) {
		return tx.ExecContext(ctx, "INSERT INTO model_configs(id,title,prompt_id,created,updated,is_default) SELECT 1,'默认视频命名',(SELECT min(id) FROM prompts),?,?,1 WHERE NOT EXISTS(SELECT 1 FROM model_configs)", storage.Now(), storage.Now())
	})
	return err
}

// columns resolves the referenced prompt via join: body and prompt_title are
// read-only views of the library entry, never stored on the config row.
const columns = "c.id,c.title,c.prompt_id,p.title,p.body,c.version,c.created,c.updated,c.is_default,c.settings,c.api_key"

func (s Store) scan(row interface{ Scan(...any) error }) (p Profile, err error) {
	var settings, sealed string
	err = row.Scan(&p.ID, &p.Title, &p.PromptID, &p.PromptTitle, &p.Body, &p.Version, &p.Created, &p.Updated, &p.Default, &settings, &sealed)
	if err != nil {
		return
	}
	p.ServerConfig = DefaultSettings()
	if err = json.Unmarshal([]byte(settings), &p.ServerConfig); err != nil {
		return
	}
	plain, err := s.db.Unseal(sealed)
	p.APIKey = secret.Value(plain)
	if err != nil {
		return
	}
	p.HasAPIKey = p.APIKey != ""
	p.Ready = p.Validate() == nil
	return
}

// Get returns model profile id.
func (s Store) Get(ctx context.Context, id int64) (Profile, error) {
	return storage.ReadValue(ctx, s.db, func(tx *sql.Tx) (Profile, error) {
		return s.scan(tx.QueryRowContext(ctx, "SELECT "+columns+" FROM model_configs c LEFT JOIN prompts p ON p.id=c.prompt_id WHERE c.id=?", id))
	})
}

// List returns all model profiles.
func (s Store) List(ctx context.Context) ([]Profile, error) {
	return storage.ReadValue(ctx, s.db, func(tx *sql.Tx) ([]Profile, error) {
		rows, err := tx.QueryContext(ctx, "SELECT "+columns+" FROM model_configs c LEFT JOIN prompts p ON p.id=c.prompt_id ORDER BY c.id")
		if err != nil {
			return nil, err
		}
		return storage.CollectRows(rows, func(rows *sql.Rows) (Profile, error) { return s.scan(rows) })
	})
}

// resolve fills the read-only prompt fields from the library so validation
// and snapshots always see the text the reference currently points at.
func (s Store) resolve(ctx context.Context, p *Profile) error {
	if p.PromptID <= 0 {
		return domain.InvalidField("prompt_id", "required")
	}
	err := s.db.View(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT title,body FROM prompts WHERE id=?", p.PromptID).Scan(&p.PromptTitle, &p.Body)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.InvalidField("prompt_id", "required")
	}
	return err
}

// Validate normalizes and checks the profile.
func (p *Profile) Validate() error {
	p.Title = strings.TrimSpace(p.Title)
	if p.Title == "" || utf8.RuneCountInString(p.Title) > 80 {
		return domain.InvalidField("title", "invalid_length")
	}
	if p.PromptID <= 0 || strings.TrimSpace(p.Body) == "" {
		return domain.InvalidField("prompt_id", "required")
	}
	var c config.TaskConfig
	if err := c.ApplyModel(p.ServerConfig, p.Body); err != nil {
		return err
	}
	p.ServerConfig = c.Server
	if p.Provider != "custom" && p.Provider != "ollama" && strings.TrimSpace(p.APIKey.Reveal()) == "" {
		return domain.InvalidField("api_key", "required")
	}
	return nil
}

// Save creates or updates profile p.
func (s Store) Save(ctx context.Context, p Profile) (Profile, error) {
	if err := s.resolve(ctx, &p); err != nil {
		return p, err
	}
	if err := p.Validate(); err != nil {
		return p, err
	}
	raw, err := json.Marshal(p.ServerConfig)
	if err != nil {
		return p, err
	}
	secret, err := s.db.Seal(p.APIKey.Reveal())
	if err != nil {
		return p, err
	}
	if p.ID == 0 {
		p, err = storage.WriteValue(ctx, s.db, func(tx *sql.Tx) (Profile, error) {
			var n int
			if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM model_configs").Scan(&n); err != nil {
				return p, err
			}
			if n >= 200 {
				return p, domain.Fail(domain.KindTooMany, domain.Failure{Reason: domain.ReasonModelConfigsFull, Message: "at most 200 model configurations"})
			}
			res, err := tx.ExecContext(ctx, "INSERT INTO model_configs(title,prompt_id,created,updated,settings,api_key) VALUES(?,?,?,?,?,?)", p.Title, p.PromptID, storage.Now(), storage.Now(), string(raw), secret)
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
			return tx.ExecContext(ctx, "UPDATE model_configs SET title=?,prompt_id=?,settings=?,api_key=?,updated=?,version=version+1 WHERE id=? AND version=?", p.Title, p.PromptID, string(raw), secret, storage.Now(), p.ID, p.Version)
		})
		if err != nil {
			return p, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return p, err
		}
		if n == 0 {
			return p, domain.Fail(domain.KindConflict, domain.Failure{Reason: domain.ReasonModelConfigChanged, Message: "model configuration was modified or deleted; refresh"})
		}
	}
	return s.Get(ctx, p.ID)
}

// ResolveKey reuses a stored key only for the same destination and protocol.
func (s Store) ResolveKey(ctx context.Context, p *Profile, key *secret.Value) error {
	if key != nil {
		p.APIKey = *key
		return nil
	}
	if p.ID == 0 {
		return nil
	}
	old, err := s.Get(ctx, p.ID)
	if err != nil {
		return err
	}
	if old.BaseURL != p.BaseURL || old.Provider != p.Provider || old.Protocol != p.Protocol {
		if old.HasAPIKey {
			return domain.InvalidField("api_key", "reenter_after_endpoint_change")
		}
		return nil
	}
	p.APIKey = old.APIKey
	return nil
}

// SetDefault makes profile id the default.
func (s Store) SetDefault(ctx context.Context, id int64) error {
	p, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if err = p.Validate(); err != nil {
		return err
	}
	return s.db.Write(ctx, func(tx *sql.Tx) error {
		var err error
		if _, err = tx.ExecContext(ctx, "UPDATE model_configs SET is_default=0 WHERE is_default=1"); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, "UPDATE model_configs SET is_default=1 WHERE id=? AND version=?", id, p.Version)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n != 1 {
			return domain.Fail(domain.KindConflict, domain.Failure{Reason: domain.ReasonModelConfigChanged, Message: "model configuration was modified or deleted; refresh"})
		}
		return nil
	})
}

// DefaultID returns the ID of the default profile.
func (s Store) DefaultID(ctx context.Context) (id int64, err error) {
	err = s.db.View(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT id FROM model_configs WHERE is_default=1").Scan(&id)
	})
	return
}

type snapshot struct {
	Settings config.ServerConfig `json:"settings"`
	Key      secret.Value        `json:"key"`
	Body     string              `json:"body"`
}

// Snapshot returns the encrypted settings, key and prompt of p for a batch.
func (s Store) Snapshot(p Profile) (string, error) {
	// Reveal is limited to this encrypted serialization boundary; the decoded
	// snapshot itself retains the redacting credential type.
	raw, err := json.Marshal(struct {
		Settings config.ServerConfig `json:"settings"`
		Key      string              `json:"key"`
		Body     string              `json:"body"`
	}{p.ServerConfig, p.APIKey.Reveal(), p.Body})
	if err != nil {
		return "", err
	}
	return s.db.Seal(string(raw))
}

// ApplySnapshot decrypts snapshot value into c.
func (s Store) ApplySnapshot(value string, c *config.TaskConfig) error {
	if value == "" {
		return domain.Fail(domain.KindInvalid, domain.Failure{Reason: domain.ReasonModelSnapshotMissing, Message: "run has no model configuration snapshot; create the run again"})
	}
	plain, err := s.db.Unseal(value)
	if err != nil {
		return err
	}
	var snap snapshot
	if err = json.Unmarshal([]byte(plain), &snap); err != nil {
		return domain.Fail(domain.KindInvalid, domain.Failure{Reason: domain.ReasonModelSnapshotInvalid, Message: "model snapshot is invalid"})
	}
	snap.Settings.APIKey = snap.Key
	return c.ApplyModel(snap.Settings, snap.Body)
}

// String prevents plaintext snapshot diagnostics.
func (s snapshot) String() string { return "[redacted snapshot]" }

// GoString prevents Go-syntax plaintext snapshot diagnostics.
func (s snapshot) GoString() string { return s.String() }

// New creates the models store.
func New(db *storage.Store) Store { return Store{db: db} }
