package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"video-rename/internal/domain"
)

func scanSnapshotDifference(phase string, before, after domain.Snapshot) error {
	return fmt.Errorf("%w（%s：身份 %s → %s，大小 %d → %d，修改时间 %s → %s）",
		ErrDirectoryScanChanged, phase, Identity(before), Identity(after), before.Size, after.Size, stamp(before), stamp(after))
}

// Report one captured difference, without rereading a possibly unstable NAS.
func scanMetadataDifference(ctx context.Context, stage *sql.DB) error {
	var name string
	var beforeKind, beforeIdentity, beforeMtime, afterKind, afterIdentity, afterMtime sql.NullString
	var beforeSize, afterSize sql.NullInt64
	err := stage.QueryRowContext(ctx, `SELECT s.name,s.kind,s.identity,s.size,s.mtime,v.kind,v.identity,v.size,v.mtime
	 FROM snapshot s LEFT JOIN verification v ON v.name=s.name
	 WHERE v.name IS NULL OR (s.kind,s.identity,s.size,s.mtime)<>(v.kind,v.identity,v.size,v.mtime)
	 UNION ALL
	 SELECT v.name,NULL,NULL,NULL,NULL,v.kind,v.identity,v.size,v.mtime
	 FROM verification v LEFT JOIN snapshot s ON s.name=v.name WHERE s.name IS NULL
	 LIMIT 1`).Scan(&name, &beforeKind, &beforeIdentity, &beforeSize, &beforeMtime,
		&afterKind, &afterIdentity, &afterSize, &afterMtime)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("%w（条目两遍读取：%q；首次：%s；复核：%s）", ErrDirectoryScanChanged, name,
		scanEntryEvidence(beforeKind, beforeIdentity, beforeSize, beforeMtime),
		scanEntryEvidence(afterKind, afterIdentity, afterSize, afterMtime))
}

func scanEntryEvidence(kind, identity sql.NullString, size sql.NullInt64, mtime sql.NullString) string {
	if !kind.Valid {
		return "缺失"
	}
	return fmt.Sprintf("类型=%s，身份=%s，大小=%d，修改时间=%s", kind.String, identity.String, size.Int64, mtime.String)
}

// The global epoch is only a hint: a refresh or mutation in another directory
// must not invalidate this scan. Recheck its path and direct children once, in
// the publication transaction. Ignore ancestor size/mtime/version, which do not
// change the path to the pinned directory. Child versions still detect ABA.
func validateScanBaseline(ctx context.Context, tx *sql.Tx, id int64, baseline scanBaseline) error {
	for _, e := range baseline.chain {
		var matches bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM entries
		 WHERE id=? AND parent=? AND name=? AND kind=? AND identity=? AND present=1)`,
			e.ID, e.Parent, e.Name, e.Kind, e.Identity).Scan(&matches); err != nil {
			return err
		}
		if !matches {
			return fmt.Errorf("%w（索引路径校验：目录 ID=%d，名称=%q，父目录 ID=%d，身份=%s 已不匹配）",
				ErrDirectoryScanChanged, e.ID, e.Name, e.Parent, e.Identity)
		}
	}
	var entry int64
	var beforeName, beforeKind, beforeIdentity, beforeMtime, afterName, afterKind, afterIdentity, afterMtime sql.NullString
	var beforeSize, beforeVersion, afterSize, afterVersion sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT e.id,b.name,b.kind,b.identity,b.size,b.mtime,b.version,
	 e.name,e.kind,e.identity,e.size,e.mtime,e.version
	 FROM entries e LEFT JOIN directory_scan.baseline b ON b.id=e.id
	 WHERE e.parent=? AND e.present=1 AND (b.id IS NULL OR
	 (e.name,e.kind,e.identity,e.size,e.mtime,e.version)<>(b.name,b.kind,b.identity,b.size,b.mtime,b.version))
	 UNION ALL
	 SELECT b.id,b.name,b.kind,b.identity,b.size,b.mtime,b.version,NULL,NULL,NULL,NULL,NULL,NULL
	 FROM directory_scan.baseline b LEFT JOIN entries e ON e.id=b.id AND e.parent=? AND e.present=1
	 WHERE e.id IS NULL LIMIT 1`, id, id).Scan(&entry,
		&beforeName, &beforeKind, &beforeIdentity, &beforeSize, &beforeMtime, &beforeVersion,
		&afterName, &afterKind, &afterIdentity, &afterSize, &afterMtime, &afterVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("%w（当前目录索引基线：条目 ID=%d；首次：名称=%q，版本=%d，%s；发布前：名称=%q，版本=%d，%s）",
		ErrDirectoryScanChanged, entry, beforeName.String, beforeVersion.Int64,
		scanEntryEvidence(beforeKind, beforeIdentity, beforeSize, beforeMtime),
		afterName.String, afterVersion.Int64, scanEntryEvidence(afterKind, afterIdentity, afterSize, afterMtime))
}
