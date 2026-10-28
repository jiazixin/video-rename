package app

import "video-rename/internal/scheduler"

type scheduleInput struct {
	Task          string  `json:"task"`
	ModelConfigID int64   `json:"model_config_id"`
	Name          string  `json:"name"`
	Targets       []int64 `json:"targets"`
	Recursive     bool    `json:"recursive"`
	Kind          string  `json:"kind"`
	Expression    string  `json:"expression"`
	Timezone      string  `json:"timezone"`
	Enabled       bool    `json:"enabled"`
}

func scheduleInputOf(p scheduler.Plan) scheduleInput {
	return scheduleInput{p.Task, p.ModelConfigID, p.Name, p.Targets, p.Recursive, p.Kind, p.Expression, p.Timezone, p.Enabled}
}
func (s scheduleInput) plan(id, version int64) scheduler.Plan {
	return scheduler.Plan{ID: id, Version: version, Task: s.Task, ModelConfigID: s.ModelConfigID, Name: s.Name, Targets: s.Targets, Recursive: s.Recursive, Kind: s.Kind, Expression: s.Expression, Timezone: s.Timezone, Enabled: s.Enabled}
}
