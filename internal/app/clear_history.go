package app

import (
	"fmt"
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
		send(w, 400, map[string]string{"message": "请选择成功或失败记录"})
		return
	}
	if (input.All && len(input.IDs) > 0) || (!input.All && (len(input.IDs) == 0 || len(input.IDs) > 200)) {
		send(w, 400, map[string]string{"message": "请选择本页记录或清空当前分类"})
		return
	}
	query := "DELETE FROM events WHERE category=?"
	args := []any{input.Category}
	if !input.All {
		for _, id := range input.IDs {
			if id < 1 {
				send(w, 400, map[string]string{"message": "记录编号无效"})
				return
			}
			args = append(args, id)
		}
		query += " AND id IN (" + strings.TrimRight(strings.Repeat("?,", len(input.IDs)), ",") + ")"
	}
	result, err := a.DB.Exec(query, args...)
	if err != nil {
		failure(w, err)
		return
	}
	n, err := result.RowsAffected()
	if err != nil {
		failure(w, err)
		return
	}
	send(w, 200, map[string]any{"deleted": n, "message": fmt.Sprintf("已清除 %d 条记录", n)})
}
