package app

import (
	"net/http"
	"strings"
)

func (a *App) clearHistory(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Category string  `json:"category"`
		All      bool    `json:"all"`
		IDs      []int64 `json:"ids"`
	}
	if !decode(w, r, &input) {
		return
	}
	if input.Category != "success" && input.Category != "failure" {
		invalid(w, r, "category", "invalid_choice")
		return
	}
	if (input.All && len(input.IDs) > 0) || (!input.All && (len(input.IDs) == 0 || len(input.IDs) > 200)) {
		invalid(w, r, "ids", "selection_required")
		return
	}
	query := "DELETE FROM events WHERE category=?"
	args := []any{input.Category}
	if !input.All {
		for _, id := range input.IDs {
			if id < 1 {
				invalid(w, r, "ids", "invalid")
				return
			}
			args = append(args, id)
		}
		query += " AND id IN (" + strings.TrimRight(strings.Repeat("?,", len(input.IDs)), ",") + ")"
	}
	result, err := a.DB.ExecContext(r.Context(), query, args...)
	if err != nil {
		writeError(w, r, err)
		return
	}
	n, err := result.RowsAffected()
	if err != nil {
		writeError(w, r, err)
		return
	}
	send(w, http.StatusOK, map[string]any{"deleted": n})
}
