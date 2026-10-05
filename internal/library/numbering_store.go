package library

import (
	"fmt"
	"path/filepath"

	"video-rename/internal/storage"
)

type Numbering struct {
	ID            string          `json:"id"`
	Parent        int64           `json:"parent"`
	Status        string          `json:"status"`
	Created       string          `json:"created"`
	UndoOf        string          `json:"undo_of"`
	Error         string          `json:"error"`
	ProcessingLog string          `json:"processing_log"`
	Total         int             `json:"total"`
	Changed       int             `json:"changed"`
	Items         []NumberingItem `json:"items,omitempty"`
	Fingerprint   string          `json:"-"`
	Media         string          `json:"-"`
}

func (l *Library) Numbering(id string) (Numbering, error) {
	var b Numbering
	err := l.DB.QueryRow("SELECT id,parent,status,created,fingerprint,undo_of,error,media FROM image_numberings WHERE id=?", id).Scan(&b.ID, &b.Parent, &b.Status, &b.Created, &b.Fingerprint, &b.UndoOf, &b.Error, &b.Media)
	if err != nil {
		return b, err
	}
	b.decodeNumberingError()
	rows, err := l.DB.Query(`SELECT entry,version,identity,size,mtime,old_name,new_name,temp_name,current_name,pending_name,operation,undo_of FROM image_numbering_items WHERE batch=? ORDER BY position`, id)
	if err != nil {
		return b, err
	}
	defer rows.Close()
	for rows.Next() {
		var i NumberingItem
		if err = rows.Scan(&i.Entry, &i.Version, &i.Identity, &i.Size, &i.Mtime, &i.Old, &i.New, &i.Temp, &i.Current, &i.Pending, &i.Operation, &i.UndoOf); err != nil {
			return b, err
		}
		b.Items = append(b.Items, i)
		if i.Old != i.New {
			b.Changed++
		}
	}
	b.Total = len(b.Items)
	return b, rows.Err()
}
func (l *Library) NumberingHistory(parent int64, media string) ([]Numbering, error) {
	if _, err := mediaOf(media); err != nil {
		return nil, err
	}
	rows, err := l.DB.Query(`SELECT b.id,b.parent,b.status,b.created,b.undo_of,b.error,count(i.entry),COALESCE(sum(i.old_name<>i.new_name),0) FROM image_numberings b JOIN image_numbering_items i ON i.batch=b.id WHERE b.parent=? AND b.media=? AND b.status<>'preview' GROUP BY b.id ORDER BY b.created DESC,b.id DESC LIMIT 50`, parent, media)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Numbering{}
	for rows.Next() {
		var b Numbering
		if err = rows.Scan(&b.ID, &b.Parent, &b.Status, &b.Created, &b.UndoOf, &b.Error, &b.Total, &b.Changed); err != nil {
			return nil, err
		}
		b.decodeNumberingError()
		result = append(result, b)
	}
	return result, rows.Err()
}
func (l *Library) PreviewNumberingRestore(id string) (Numbering, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	original, err := l.Numbering(id)
	if err != nil {
		return Numbering{}, err
	}
	if original.Status != "committed" || original.UndoOf != "" {
		return Numbering{}, fmt.Errorf("该批次不可还原")
	}
	if err = l.CheckAvailable(original.Parent); err != nil {
		return Numbering{}, err
	}
	path, err := l.Path(original.Parent)
	if err != nil {
		return Numbering{}, err
	}
	fingerprint, err := directoryFingerprint(path)
	if err != nil {
		return Numbering{}, err
	}
	b := Numbering{ID: numberingToken(), Parent: original.Parent, Created: storage.Now(), Status: "preview", Fingerprint: fingerprint, UndoOf: id, Media: original.Media}
	for _, i := range original.Items {
		if i.Old == i.New {
			continue
		}
		e, err := l.Get(i.Entry)
		if err != nil {
			return Numbering{}, err
		}
		if e.Parent != b.Parent || e.Name != i.New || e.RestoreID != i.Operation || !numberingMatch(filepath.Join(path, e.Name), i) {
			return Numbering{}, fmt.Errorf("%s %s 已变化或有后续改名，不能整批还原", mediaLabel(b.Media), i.New)
		}
		b.Items = append(b.Items, NumberingItem{Entry: e.ID, Version: e.Version, Identity: i.Identity, Size: i.Size, Mtime: i.Mtime, Old: i.New, New: i.Old, UndoOf: i.Operation})
	}
	if len(b.Items) == 0 {
		return Numbering{}, fmt.Errorf("该批次没有需要还原的%s", mediaLabel(b.Media))
	}
	return l.saveNumberingPreview(b, path)
}
