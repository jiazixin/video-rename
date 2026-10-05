package app

import (
	"fmt"
	"net/http"
	"video-rename/internal/models"
	"video-rename/internal/systemsettings"
)

func (a *App) systemSettingsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/system", func(w http.ResponseWriter, r *http.Request) {
		_, _, err := a.Jobs.Tools()
		message := ""
		if err != nil {
			message = err.Error()
		}
		model := ""
		configured := false
		store := models.Store{DB: a.DB}
		profiles, modelErr := store.List()
		if modelErr != nil {
			failure(w, modelErr)
			return
		}
		for _, p := range profiles {
			if p.Ready {
				configured = true
			}
			if p.Default {
				model = p.Model
			}
		}
		if !configured && message == "" {
			message = "请先在模型配置页面完善一个模型"
		}
		send(w, 200, map[string]any{"root_id": 1, "model": model, "ready": err == nil && configured, "message": message})
	})
	store := systemsettings.Store{DB: a.DB}
	mux.HandleFunc("GET /api/system/settings", func(w http.ResponseWriter, r *http.Request) {
		s, err := store.Get()
		if err != nil {
			failure(w, err)
			return
		}
		send(w, 200, map[string]any{"settings": s, "cache": a.Jobs.Cache.Stats(), "retention_days": float64(s.IdleCacheHours) / 24})
	})
	mux.HandleFunc("PUT /api/system/settings", func(w http.ResponseWriter, r *http.Request) {
		s := systemsettings.Default()
		s.ExtractWorkers, s.CacheGB = 0, 0
		if !decode(w, r, &s) {
			return
		}
		a.settingsMu.Lock()
		defer a.settingsMu.Unlock()
		if s.FallbackModelID > 0 {
			p, err := (models.Store{DB: a.DB}).Get(s.FallbackModelID)
			if err != nil || !p.Ready {
				failure(w, fmt.Errorf("请选择已配置完成的兜底模型"))
				return
			}
		}
		if err := store.Save(s); err != nil {
			failure(w, err)
			return
		}
		if err := a.Jobs.Cache.Configure(s.Bytes(), s.Policy); err != nil {
			failure(w, err)
			return
		}
		send(w, 200, s)
	})
	mux.HandleFunc("POST /api/system/cache/clear", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Confirm bool `json:"confirm"`
		}
		if !decode(w, r, &body) {
			return
		}
		if !body.Confirm {
			failure(w, fmt.Errorf("请确认清空闲置截图缓存"))
			return
		}
		result, err := a.Jobs.Cache.Clear()
		if err != nil {
			failure(w, fmt.Errorf("已清理 %d 项，后续清理失败：%w", result.Deleted, err))
			return
		}
		send(w, 200, result)
	})
}
