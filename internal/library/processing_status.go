package library

import "fmt"

// These expressions share the live state used by Get, filtering, and sorting.
// They expect the current entry to be aliased as e.
const entryProcessedSQL = `COALESCE((SELECT source FROM marks m WHERE m.identity=e.identity AND m.size=e.size AND m.mtime=e.mtime),'')`

const entryTaskStatusSQL = `COALESCE((SELECT CASE WHEN r.status='paused' THEN 'paused' ELSE i.status END FROM run_items i JOIN runs r ON r.id=i.run WHERE i.entry=e.id AND i.status IN ('queued','running') AND r.status IN ('queued','scanning','running','paused','cancelling') ORDER BY i.id DESC LIMIT 1),
 (WITH RECURSIVE ancestors(id,parent,depth) AS (
 SELECT e.id,e.parent,0 UNION ALL SELECT p.id,p.parent,a.depth+1 FROM entries p JOIN ancestors a ON p.id=a.parent WHERE a.depth<1024
 ) SELECT CASE WHEN e.kind='directory' OR r.status='paused' THEN r.status ELSE 'queued' END
 FROM runs r JOIN json_each(r.targets) target JOIN entries t ON t.id=target.value
 WHERE r.status IN ('queued','scanning','running','paused','cancelling')
 AND (t.id=e.id OR (t.kind='file' AND e.kind='file' AND t.identity=e.identity) OR (t.kind='directory' AND EXISTS(SELECT 1 FROM ancestors a WHERE a.id=t.id AND (r.recursive=1 OR (a.depth=1 AND e.kind='file')))))
 AND (e.kind='directory' OR NOT EXISTS(SELECT 1 FROM run_items i WHERE i.run=r.id AND i.identity=e.identity))
 ORDER BY r.id LIMIT 1),'')`

const directoryProcessingStatusSQL = `COALESCE((SELECT COALESCE(NULLIF(` + entryTaskStatusSQL + `,''),NULLIF(` + entryProcessedSQL + `,''),'unprocessed') FROM entries e WHERE e.id=directory_items.entry AND e.present=1),'unprocessed')`

// processingStatusFilter matches file state. List queries separately retain
// directories for navigation; numbering keeps hidden files as reserved names.
func processingStatusFilter(status string) (string, error) {
	if status == "" {
		return "1", nil
	}
	var match string
	switch status {
	case "processed":
		match = " IN ('marked','manual','automatic')"
	case "active":
		match = " IN ('queued','scanning','running','paused','cancelling')"
	case "unprocessed", "marked", "manual", "automatic", "restore", "queued", "scanning", "running", "paused", "cancelling":
		match = "='" + status + "'"
	default:
		return "", fmt.Errorf("无效的处理状态")
	}
	return directoryProcessingStatusSQL + match, nil
}

const directoryStatusOrderSQL = `CASE WHEN kind='directory' THEN 0 ELSE CASE ` + directoryProcessingStatusSQL + `
 WHEN 'unprocessed' THEN 0
 WHEN 'queued' THEN 1
 WHEN 'scanning' THEN 2
 WHEN 'running' THEN 3
 WHEN 'paused' THEN 4
 WHEN 'cancelling' THEN 5
 WHEN 'marked' THEN 6
 WHEN 'manual' THEN 7
 WHEN 'automatic' THEN 8
 WHEN 'restore' THEN 9
 ELSE 10 END END`
