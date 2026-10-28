package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"video-rename/internal/domain"
	"video-rename/internal/storage"
)

func scanSnapshotDifference(phase string, before, after domain.Snapshot) error {
	return ErrDirectoryScanChanged.Wrap(fmt.Errorf("%s: identity %s -> %s, size %d -> %d, mtime_ns %d -> %d",
		phase, Identity(before), Identity(after), before.Size, after.Size, stamp(before), stamp(after)))
}

// Report one captured difference, without rereading a possibly unstable NAS.
func scanMetadataDifference(ctx context.Context, stage *storage.ScanStore) error {
	var name string
	var found bool
	var beforeKind, beforeIdentity, afterKind, afterIdentity sql.NullString
	var beforeSize, beforeMtime, afterSize, afterMtime sql.NullInt64
	err := stage.View(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `SELECT s.name,s.kind,s.identity,s.size,s.mtime_ns,v.kind,v.identity,v.size,v.mtime_ns
	 FROM snapshot s LEFT JOIN verification v ON v.name=s.name
	 WHERE v.name IS NULL OR (s.kind,s.identity,s.size,s.mtime_ns)<>(v.kind,v.identity,v.size,v.mtime_ns)
	 UNION ALL
	 SELECT v.name,NULL,NULL,NULL,NULL,v.kind,v.identity,v.size,v.mtime_ns
	 FROM verification v LEFT JOIN snapshot s ON s.name=v.name WHERE s.name IS NULL
	 LIMIT 1`).Scan(&name, &beforeKind, &beforeIdentity, &beforeSize, &beforeMtime,
			&afterKind, &afterIdentity, &afterSize, &afterMtime)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		found = err == nil
		return err
	})
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	return ErrDirectoryScanChanged.Wrap(fmt.Errorf("entry read twice: %q; first: %s; second: %s", name,
		scanEntryEvidence(scanEntryEvidenceOptions{Kind: beforeKind, Identity: beforeIdentity, Size: beforeSize, Mtime: beforeMtime}),
		scanEntryEvidence(scanEntryEvidenceOptions{Kind: afterKind, Identity: afterIdentity, Size: afterSize, Mtime: afterMtime})))
}

// scanEntryEvidenceOptions groups the inputs for scanEntryEvidence.
type scanEntryEvidenceOptions struct {
	Kind     sql.NullString
	Identity sql.NullString
	Size     sql.NullInt64
	Mtime    sql.NullInt64
}

func scanEntryEvidence(params scanEntryEvidenceOptions) string {
	kind, identity, size, mtime := params.Kind, params.Identity, params.Size, params.Mtime

	if !kind.Valid {
		return "missing"
	}
	return fmt.Sprintf("kind=%s, identity=%s, size=%d, mtime_ns=%d", kind.String, identity.String, size.Int64, mtime.Int64)
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
			return ErrDirectoryScanChanged.Wrap(fmt.Errorf("indexed path check: directory ID=%d, name=%q, parent ID=%d, identity=%s no longer match",
				e.ID, e.Name, e.Parent, e.Identity))
		}
	}
	var entry int64
	var beforeName, beforeKind, beforeIdentity, afterName, afterKind, afterIdentity sql.NullString
	var beforeSize, beforeMtime, beforeVersion, afterSize, afterMtime, afterVersion sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT e.id,b.name,b.kind,b.identity,b.size,b.mtime_ns,b.version,
	 e.name,e.kind,e.identity,e.size,e.mtime_ns,e.version
	 FROM entries e LEFT JOIN directory_scan.baseline b ON b.id=e.id
	 WHERE e.parent=? AND e.present=1 AND (b.id IS NULL OR
	 (e.name,e.kind,e.identity,e.size,e.mtime_ns,e.version)<>(b.name,b.kind,b.identity,b.size,b.mtime_ns,b.version))
	 UNION ALL
	 SELECT b.id,b.name,b.kind,b.identity,b.size,b.mtime_ns,b.version,NULL,NULL,NULL,NULL,NULL,NULL
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
	return ErrDirectoryScanChanged.Wrap(fmt.Errorf("directory index baseline: entry ID=%d; first: name=%q, version=%d, %s; before publishing: name=%q, version=%d, %s",
		entry, beforeName.String, beforeVersion.Int64,
		scanEntryEvidence(scanEntryEvidenceOptions{Kind: beforeKind, Identity: beforeIdentity, Size: beforeSize, Mtime: beforeMtime}),
		afterName.String, afterVersion.Int64, scanEntryEvidence(scanEntryEvidenceOptions{Kind: afterKind, Identity: afterIdentity, Size: afterSize, Mtime: afterMtime})))
}
