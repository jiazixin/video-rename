package library

import (
	"context"
	"database/sql"
	"encoding/json/v2"

	"video-rename/internal/domain"
)

// These expressions share the live state used by Get, filtering, and sorting.
// They expect the current entry to be aliased as e.
const entryProcessedSQL = `COALESCE((SELECT source FROM marks m WHERE m.identity=e.identity AND m.size=e.size AND m.mtime=e.mtime),'')`

const entryTaskStatusSQL = `COALESCE((SELECT CASE WHEN r.status='paused' THEN 'paused' ELSE i.status END FROM run_items i JOIN runs r ON r.id=i.run WHERE i.entry=e.id AND i.status IN ('queued','running') AND r.status IN ` + domain.ActiveRunStatusesSQL + ` ORDER BY i.id DESC LIMIT 1),
 (WITH RECURSIVE ancestors(id,parent,depth) AS (
 SELECT e.id,e.parent,0 UNION ALL SELECT p.id,p.parent,a.depth+1 FROM entries p JOIN ancestors a ON p.id=a.parent WHERE a.depth<1024
 ) SELECT CASE WHEN e.kind='directory' OR r.status='paused' THEN r.status ELSE 'queued' END
 FROM runs r INDEXED BY run_active JOIN json_each(r.targets) target JOIN entries t ON t.id=target.value
 WHERE r.status IN ` + domain.ActiveRunStatusesSQL + `
 AND (t.id=e.id OR (t.kind='file' AND e.kind='file' AND t.identity=e.identity) OR (t.kind='directory' AND EXISTS(SELECT 1 FROM ancestors a WHERE a.id=t.id AND (r.recursive=1 OR (a.depth=1 AND e.kind='file')))))
 AND (e.kind='directory' OR NOT EXISTS(SELECT 1 FROM run_items i WHERE i.run=r.id AND i.identity=e.identity))
 ORDER BY r.id LIMIT 1),'')`

const directoryProcessingStatusSQL = `COALESCE((SELECT COALESCE(NULLIF(` + entryTaskStatusSQL + `,''),NULLIF(` + entryProcessedSQL + `,''),'unprocessed') FROM entries e WHERE e.id=directory_items.entry AND e.present=1),'unprocessed')`

// processingStatusFilter matches file state; directory lists separately match
// recursive video progress. Numbering keeps hidden files as reserved names.
func processingStatusFilter(status string) (string, error) {
	if status == "" {
		return "1", nil
	}
	match, err := processingStatusComparison(status)
	if err != nil {
		return "", err
	}
	return directoryProcessingStatusSQL + match, nil
}

func processingStatusComparison(status string) (string, error) {
	var match string
	switch status {
	case "processed":
		match = " IN ('marked','manual','automatic')"
	case "active":
		match = " IN " + domain.ActiveRunStatusesSQL
	case "unprocessed", "marked", "manual", "automatic", "restore", "queued", "scanning", "running", "paused", "cancelling":
		match = "='" + status + "'"
	default:
		return "", domain.InvalidField("status", "invalid_choice")
	}
	return match, nil
}

func (n *directoryProcessing) matchesStatus(status string) bool {
	if n.taskStatus != "" {
		return status == "active" || status == n.taskStatus
	}
	switch status {
	case "unprocessed":
		return n.progress.Unknown > 0 || n.progress.Processed < n.progress.Total
	case "processed":
		return n.progress.Unknown == 0 && n.progress.Total > 0 && n.progress.Processed == n.progress.Total
	default:
		return n.matches > 0
	}
}

// directoryStatusIDs evaluates sibling subtrees together, before count or page
// boundaries are calculated. Unknown subtrees remain discoverable as unprocessed.
func directoryStatusIDs(ctx context.Context, tx *sql.Tx, id, generation int64, query, status string) (string, error) {
	nodes, err := directoryStatusNodes(ctx, tx, id, generation, query, status)
	if err != nil {
		return "", err
	}
	matched := []int64{}
	for id, n := range nodes {
		if n.matchesStatus(status) {
			matched = append(matched, id)
		}
	}
	raw, err := json.Marshal(matched)
	return string(raw), err
}

// Read all matching siblings before paging so subtree state determines both
// numbered pages and cursor boundaries, rather than sorting only visible rows.
func directoryStatusNodes(ctx context.Context, tx *sql.Tx, id, generation int64, query, status string) (map[int64]*directoryProcessing, error) {
	rows, err := tx.QueryContext(ctx, `SELECT entry FROM directory_items WHERE dir=? AND generation=? AND kind='directory' AND instr(lower(name),lower(?))>0`, id, generation, query)
	if err != nil {
		return nil, err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	selected := map[int64]*directoryProcessing{}
	if len(ids) == 0 {
		return selected, nil
	}
	nodes, err := readDirectoryProcessing(ctx, tx, ids, status)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if n := nodes[id]; n != nil {
			selected[id] = n
		}
	}
	return selected, nil
}

// Match DirectoryStatus.vue: active task state overrides video progress.
// Ascending: unknown, empty, unprocessed, queued/scanning/running/paused/
// cancelling, partially processed, fully processed.
func (n *directoryProcessing) sortRank() int {
	switch n.taskStatus {
	case "queued":
		return 3
	case "scanning":
		return 4
	case "running":
		return 5
	case "paused":
		return 6
	case "cancelling":
		return 7
	}
	switch {
	case n.progress.Unknown > 0:
		return 0
	case n.progress.Total == 0:
		return 1
	case n.progress.Processed == 0:
		return 2
	case n.progress.Processed < n.progress.Total:
		return 8
	default:
		return 9
	}
}

func directoryStatusRanks(ctx context.Context, tx *sql.Tx, id, generation int64, query string) (string, error) {
	// The processed filter requests live task state without source-match counts.
	nodes, err := directoryStatusNodes(ctx, tx, id, generation, query, "processed")
	if err != nil {
		return "", err
	}
	ranks := map[int64]int{}
	for id, n := range nodes {
		ranks[id] = n.sortRank()
	}
	raw, err := json.Marshal(ranks)
	return string(raw), err
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
