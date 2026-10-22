package library

import (
	"fmt"
	"os"
	"path/filepath"

	"video-rename/internal/domain"
)

// Delete permanently removes an entry and its contents; rename history is retained.
func (l *Library) Delete(id, version int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.broken != nil {
		return fmt.Errorf("操作已停止，需要重启恢复: %w", l.broken)
	}
	e, err := l.Get(id)
	if err != nil {
		return err
	}
	if id == 1 || (e.Kind != "file" && e.Kind != "directory") {
		return fmt.Errorf("不能删除媒体根目录、符号链接或特殊文件")
	}
	if e.Version != version {
		return fmt.Errorf("文件已变化，请刷新后重新确认删除")
	}
	if err := l.CheckAvailable(id); err != nil {
		return err
	}
	if e.TaskStatus != "" || (l.Busy != nil && l.Busy(id)) {
		return fmt.Errorf("该文件或目录有活动任务，请等待任务完成")
	}
	var active int
	if err := l.DB.QueryRow(`WITH RECURSIVE descendants(id) AS (
	 SELECT ? UNION ALL SELECT e.id FROM entries e JOIN descendants d ON e.parent=d.id WHERE e.present=1
	) SELECT count(*) FROM runs r JOIN json_each(r.targets) t
	 WHERE r.status IN `+domain.ActiveRunStatusesSQL+` AND t.value IN (SELECT id FROM descendants)`, id).Scan(&active); err != nil {
		return err
	}
	if active > 0 {
		return fmt.Errorf("目录内有活动任务，请等待任务完成")
	}
	p, err := l.Path(id)
	if err != nil {
		return err
	}
	snap, err := domain.StatSnapshot(p)
	if err != nil {
		return err
	}
	if Identity(snap) != e.Identity || (e.Kind == "file" && (snap.Size != e.Size || stamp(snap) != e.Mtime)) {
		return fmt.Errorf("文件已变化，请刷新后重新确认删除")
	}
	root, err := os.OpenRoot(l.Root)
	if err != nil {
		return err
	}
	defer root.Close()
	rel, err := filepath.Rel(l.Root, p)
	if err != nil {
		return err
	}
	if _, err := l.Path(id); err != nil {
		return err
	}
	l.indexEpoch.Add(1)
	if err := root.RemoveAll(rel); err != nil {
		return fmt.Errorf("删除未完成，目录可能已部分删除，请刷新后核对: %w", err)
	}
	_, err = l.DB.Exec(`WITH RECURSIVE descendants(id) AS (
	 SELECT ? UNION ALL SELECT e.id FROM entries e JOIN descendants d ON e.parent=d.id
	) UPDATE entries SET present=0,version=version+1 WHERE id IN (SELECT id FROM descendants)`, id)
	if err != nil {
		l.broken = err
		return fmt.Errorf("文件已删除，但索引更新失败，请重启并刷新目录: %w", err)
	}
	if _, err := l.DB.Exec("DELETE FROM directory_items WHERE entry IN (SELECT id FROM entries WHERE present=0)"); err != nil {
		return fmt.Errorf("文件已删除，列表更新失败，请刷新目录: %w", err)
	}
	return nil
}
