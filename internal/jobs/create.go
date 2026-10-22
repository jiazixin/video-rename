package jobs

import (
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"strings"

	"video-rename/internal/config"
	"video-rename/internal/discovery"
	"video-rename/internal/models"
	"video-rename/internal/sampling"
	"video-rename/internal/storage"
	"video-rename/internal/systemsettings"
)

// Create queues a run, or returns the run already created with the same
// request key when the key was used for exactly the same request.
func (m *Manager) Create(r Request, source string, plan int64) (int64, error) {
	m.createMu.Lock()
	defer m.createMu.Unlock()
	if len(r.Targets) == 0 || len(r.Key) < 8 || len(r.Key) > 180 {
		return 0, fmt.Errorf("请选择目录或文件，并提供有效请求键")
	}
	if r.Task == "" {
		r.Task = "rename"
	}
	if r.Task != "rename" && r.Task != "extract" {
		return 0, fmt.Errorf("请选择重命名或抽帧任务")
	}
	if r.Task == "extract" {
		r.ModelConfigID = 0
	}
	// Normalize scopes without scanning them. Overlapping recursive targets are redundant.
	index := newScopeIndex(m.DB)
	normalized, err := index.normalize(r.Targets, r.Recursive)
	if err != nil {
		return 0, err
	}
	r.Targets = normalized
	if err = validateRetryOperations(r, source, plan); err != nil {
		return 0, err
	}
	if r.Task == "rename" && r.ModelConfigID == 0 {
		if r.ModelConfigID, err = (models.Store{DB: m.DB}).DefaultID(); err != nil {
			return 0, err
		}
	}
	if existing, found, err := m.existingRun(r); err != nil || found {
		return existing, err
	}
	if err = m.checkOverlap(r, index); err != nil {
		return 0, err
	}
	for entry, operation := range r.RetryOperations {
		if err := m.Library.CheckAutomaticRetry(entry, operation); err != nil {
			return 0, err
		}
	}
	if _, _, err = m.Tools(); err != nil {
		return 0, err
	}
	for _, id := range r.Targets {
		e, err := index.get(id)
		if err != nil {
			return 0, err
		}
		if e.kind != "directory" && (e.kind != "file" || !discovery.IsVideoExtension(e.name)) {
			return 0, fmt.Errorf("只能运行目录或视频文件")
		}
	}
	frames, err := (sampling.Store{DB: m.DB}).Get()
	if err != nil {
		return 0, err
	}
	frameSnapshot, err := json.Marshal(frames)
	if err != nil {
		return 0, err
	}
	var cfg config.TaskConfig
	cfg.Sampling, cfg.Images = frames.Sampling, frames.Images
	var prompt models.Profile
	var modelSnapshot string
	if r.Task == "rename" {
		if prompt, modelSnapshot, err = m.freezeModel(r.ModelConfigID, &cfg); err != nil {
			return 0, err
		}
	}
	retry, err := m.freezeRetry(r.Task, r.ModelConfigID)
	if err != nil {
		return 0, err
	}
	retry.RetryOperations = maps.Clone(r.RetryOperations)
	retryRaw, err := json.Marshal(retry)
	if err != nil {
		return 0, err
	}
	targets, _ := json.Marshal(r.Targets)
	summary := runConfigSummary(r.Task, &cfg, retry)
	tx, err := m.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	limits, err := systemsettings.ReadBatchLimits(tx.QueryRow("SELECT policy FROM system_settings WHERE id=1"))
	if err != nil {
		return 0, err
	}
	var waiting int
	if err = tx.QueryRow("SELECT count(*) FROM runs WHERE status='queued' OR (status='paused' AND started='')").Scan(&waiting); err != nil {
		return 0, err
	}
	if waiting >= limits.WaitingBatchLimit {
		return 0, fmt.Errorf("等待批次已达上限（%d 个），请稍后重试或在系统设置中调整", limits.WaitingBatchLimit)
	}
	res, err := tx.Exec("INSERT INTO runs(source,targets,recursive,status,created,request_key,plan,config_summary,model_config_id,model_config_title,prompt_body,model_snapshot,sampling_snapshot,task,retry_snapshot) VALUES(?,?,?,'queued',?,?,?,?,?,?,?,?,?,?,?)", source, string(targets), r.Recursive, storage.Now(), r.Key, plan, summary, prompt.ID, prompt.Title, prompt.Body, modelSnapshot, string(frameSnapshot), r.Task, string(retryRaw))
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return id, tx.Commit()
}

// validateRetryOperations limits success-record retries to explicitly chosen
// files, each paired with the naming operation it replaces.
func validateRetryOperations(r Request, source string, plan int64) error {
	if len(r.RetryOperations) == 0 {
		return nil
	}
	if r.Task != "rename" || r.Recursive || source != "manual" || plan != 0 || len(r.RetryOperations) != len(r.Targets) {
		return fmt.Errorf("成功记录重试只能用于明确选中的视频文件")
	}
	for _, entry := range r.Targets {
		if r.RetryOperations[entry] <= 0 {
			return fmt.Errorf("成功记录重试缺少对应的命名操作")
		}
	}
	return nil
}

// existingRun finds the run created earlier with r's request key. Reusing a key
// for a different request is an error rather than a second run.
func (m *Manager) existingRun(r Request) (id int64, found bool, err error) {
	var oldTargets, oldTask, oldRetryRaw string
	var oldRecursive bool
	var oldModelConfigID int64
	err = m.DB.QueryRow("SELECT id,targets,recursive,model_config_id,task,retry_snapshot FROM runs WHERE request_key=?", r.Key).Scan(&id, &oldTargets, &oldRecursive, &oldModelConfigID, &oldTask, &oldRetryRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	var oldRetry retryPolicy
	if oldRetryRaw != "" {
		if err := json.Unmarshal([]byte(oldRetryRaw), &oldRetry); err != nil {
			return 0, false, err
		}
	}
	current, _ := json.Marshal(r.Targets)
	if oldTargets != string(current) || oldRecursive != r.Recursive || oldModelConfigID != r.ModelConfigID || oldTask != r.Task || !maps.Equal(oldRetry.RetryOperations, r.RetryOperations) {
		return 0, false, fmt.Errorf("请求键已被不同的运行范围、任务类型、模型配置或重试记录使用")
	}
	return id, true, nil
}

// freezeModel loads the chosen model configuration into cfg and returns the
// snapshot the run replays.
func (m *Manager) freezeModel(id int64, cfg *config.TaskConfig) (models.Profile, string, error) {
	prompt, err := (models.Store{DB: m.DB}).Get(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return prompt, "", fmt.Errorf("所选模型配置不存在或已删除，请重新选择模型配置")
		}
		return prompt, "", fmt.Errorf("请选择有效的模型配置: %w", err)
	}
	if err = prompt.Validate(); err != nil {
		return prompt, "", err
	}
	fixed, err := (systemsettings.Store{DB: m.DB}).FixedPrompt()
	if err != nil {
		return prompt, "", err
	}
	// The fixed prompt rides below the selected prompt as a hard constraint;
	// the snapshot stores the combined text so the run replays exactly what
	// was sent, regardless of later edits to the library or the fixed part.
	prompt.Body = strings.TrimRight(prompt.Body, "\n") + "\n\n" + strings.TrimRight(fixed, "\n")
	if err = cfg.ApplyModel(prompt.ServerConfig, prompt.Body); err != nil {
		return prompt, "", fmt.Errorf("请完善模型配置: %w", err)
	}
	snapshot, err := (models.Store{DB: m.DB}).Snapshot(prompt)
	return prompt, snapshot, err
}

// runConfigSummary is the credential-free configuration shown in batch details.
func runConfigSummary(task string, cfg *config.TaskConfig, retry retryPolicy) string {
	if task == "extract" {
		summary, _ := json.Marshal(map[string]any{"sampling": cfg.Sampling, "images": cfg.Images})
		return string(summary)
	}
	roles := []string{"system", "user"}
	systemLocation := "messages"
	if cfg.Server.Protocol == "anthropic" {
		roles = []string{"user"}
		systemLocation = "top_level"
	}
	tokenParameter := "max_tokens"
	if cfg.Server.Provider == "openai" {
		tokenParameter = "max_completion_tokens"
	}
	summary, _ := json.Marshal(map[string]any{"retry_enabled": retry.Enabled, "retry_count": retry.Count, "fallback_model_title": retry.FallbackTitle, "failure_keywords": retry.FailureKeywords, "model": cfg.Server.Model, "provider": cfg.Server.Provider, "base_url": cfg.Server.BaseURL, "protocol": cfg.Server.Protocol, "omit_temperature": cfg.Server.OmitTemperature, "thinking": cfg.Server.Thinking, "thinking_adapter": cfg.Server.ThinkingAdapter, "thinking_effort": cfg.Server.ThinkingEffort, "thinking_budget": cfg.Server.ThinkingBudget, "prompt_hash": cfg.PromptHash(), "sampling": cfg.Sampling, "images": cfg.Images, "workers": cfg.Workers, "temperature": cfg.Server.Temperature, "max_tokens": cfg.Server.MaxTokens, "token_parameter": tokenParameter, "stream": false, "message_roles": roles, "system_location": systemLocation})
	return string(summary)
}
