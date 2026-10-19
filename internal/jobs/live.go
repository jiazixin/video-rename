package jobs

import "time"

// LiveStage is the current processing step of a running item. It lives only in
// memory: restart marks running items cancelled, so a stage never outlives its
// worker and needs no schema column.
type LiveStage struct {
	// extracting, model_wait, model, retry_wait or committing.
	Stage   string `json:"stage"`
	SinceMS int64  `json:"since_ms"`
	// Attempt is the model request in progress or about to start (1-based).
	Attempt  int  `json:"attempt"`
	Fallback bool `json:"fallback"`
	Frames   int  `json:"frames"`
	// Title is the cleaned proposed name once a model request succeeded.
	Title string `json:"title"`
}

func (m *Manager) updateStage(item int64, update func(*LiveStage)) {
	m.liveMu.Lock()
	defer m.liveMu.Unlock()
	if m.live == nil {
		m.live = map[int64]LiveStage{}
	}
	s := m.live[item]
	previous := s.Stage
	update(&s)
	if s.Stage != previous {
		s.SinceMS = time.Now().UnixMilli()
	}
	m.live[item] = s
}

func (m *Manager) setStage(item int64, stage string) {
	m.updateStage(item, func(s *LiveStage) { s.Stage = stage })
}

func (m *Manager) clearStage(item int64) {
	m.liveMu.Lock()
	delete(m.live, item)
	m.liveMu.Unlock()
}

// Live returns the stages of the given items that are still being processed.
func (m *Manager) Live(items []int64) map[int64]LiveStage {
	m.liveMu.Lock()
	defer m.liveMu.Unlock()
	out := make(map[int64]LiveStage, len(items))
	for _, id := range items {
		if s, ok := m.live[id]; ok {
			out[id] = s
		}
	}
	return out
}
