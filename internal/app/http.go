package app

import (
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"strconv"
	"video-rename/internal/auth"
)

func send(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.MarshalWrite(w, v)
}
func failure(w http.ResponseWriter, err error) {
	status := 409
	code := "CONFLICT"
	message := err.Error()
	if errors.Is(err, sql.ErrNoRows) {
		status = 404
		code = "NOT_FOUND"
		message = "请求的记录不存在或已删除，请刷新页面后重试"
	}
	send(w, status, map[string]string{"code": code, "message": message, "request_id": auth.Token()})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	d := jsontext.NewDecoder(r.Body)
	if err := json.UnmarshalDecode(d, v, json.RejectUnknownMembers(true)); err != nil {
		send(w, 400, map[string]string{"message": "请求字段或格式无效"})
		return false
	}
	if _, err := d.ReadValue(); !errors.Is(err, io.EOF) {
		send(w, 400, map[string]string{"message": "请求只能包含一个 JSON 对象"})
		return false
	}
	return true
}
func number(s string) int64 { n, _ := strconv.ParseInt(s, 10, 64); return n }

// creationOrUpdateID keeps malformed PATCH paths from becoming creates (ID 0).
func creationOrUpdateID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	if r.Method == http.MethodPost {
		return 0, true
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		send(w, http.StatusBadRequest, map[string]string{"message": "记录编号无效"})
		return 0, false
	}
	return id, true
}
