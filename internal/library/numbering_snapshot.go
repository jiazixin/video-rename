package library

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"video-rename/internal/domain"
)

// Stored in the existing fingerprint column. Keep the exact observations used
// for validation, including non-participating files and child directories.
type numberingDirectorySnapshot struct {
	Version int                      `json:"version"`
	Hash    string                   `json:"hash"`
	Entries []numberingEntrySnapshot `json:"entries"`
}
type numberingEntrySnapshot struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Identity   string `json:"identity"`
	Size       int64  `json:"size"`
	ModifiedNS int64  `json:"mtime_ns"`
}

// Read bounded pages and observe cancellation between filesystem operations.
// A filesystem call already in progress must return before cancellation is seen.
func scanNumberingDirectory(ctx context.Context, path string, visit func(os.DirEntry) error) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, dir.Close()) }()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries, readErr := dir.ReadDir(refreshPageSize)
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := visit(entry); err != nil {
				return err
			}
		}
		if errors.Is(readErr, io.EOF) {
			return ctx.Err()
		}
		if readErr != nil {
			return readErr
		}
	}
}

func directoryFingerprint(ctx context.Context, path string) (string, error) {
	snapshot := numberingDirectorySnapshot{Version: 4, Entries: []numberingEntrySnapshot{}}
	err := scanNumberingDirectory(ctx, path, func(e os.DirEntry) error {
		info, err := e.Info()
		if err != nil {
			return err
		}
		snap, err := domain.SnapshotFromInfo(info)
		if err != nil {
			return err
		}
		entry := numberingEntrySnapshot{Name: e.Name(), Type: info.Mode().Type().String(), Identity: Identity(snap)}
		// Descendant contents and permission bits are not part of this rename plan.
		if !info.IsDir() {
			entry.Size = info.Size()
			entry.ModifiedNS = stamp(snap)
		}
		snapshot.Entries = append(snapshot.Entries, entry)
		return nil
	})
	if err != nil {
		return "", err
	}
	// ReadDir pages follow filesystem order; snapshots use lexical name order.
	slices.SortFunc(snapshot.Entries, func(a, b numberingEntrySnapshot) int {
		return cmp.Compare(a.Name, b.Name)
	})
	hash := sha256.New()
	for _, entry := range snapshot.Entries {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		fmt.Fprintf(hash, "%q:%q:%q:%d:%d\n", entry.Name, entry.Type, entry.Identity, entry.Size, entry.ModifiedNS)
	}
	snapshot.Hash = hex.EncodeToString(hash.Sum(nil))
	raw, err := json.Marshal(snapshot)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return string(raw), err
}
func decodeNumberingSnapshot(raw string) (numberingDirectorySnapshot, bool) {
	var snapshot numberingDirectorySnapshot
	err := json.Unmarshal([]byte(raw), &snapshot)
	return snapshot, err == nil && snapshot.Version == 4 && snapshot.Hash != ""
}
func numberingFingerprintLabel(raw string) string {
	if snapshot, ok := decodeNumberingSnapshot(raw); ok {
		return fmt.Sprintf("v%d:%s (%d entries)", snapshot.Version, snapshot.Hash, len(snapshot.Entries))
	}
	return "invalid directory snapshot"
}

// Compare the captured snapshots, not a second filesystem read that might
// already reflect another cache refresh. No retry is used to bypass a failure.
func describeNumberingSnapshotDiff(detail *strings.Builder, before, after string) bool {
	old, ok := decodeNumberingSnapshot(before)
	if !ok {
		return false
	}
	current, ok := decodeNumberingSnapshot(after)
	if !ok {
		return false
	}
	byName := make(map[string]numberingEntrySnapshot, len(current.Entries))
	for _, e := range current.Entries {
		byName[e.Name] = e
	}
	count := 0
	report := func(format string, args ...any) {
		if count < 20 {
			fmt.Fprintf(detail, format, args...)
		}
		count++
	}
	for _, a := range old.Entries {
		b, exists := byName[a.Name]
		if !exists {
			report("entry %q: missing from the current listing; preview kind=%s, identity=%s\n", a.Name, a.Type, a.Identity)
			continue
		}
		delete(byName, a.Name)
		if a != b {
			report("entry %q: kind %s -> %s; identity (dev:inode) %s -> %s; size %d -> %d; mtime_ns %d -> %d\n", a.Name, a.Type, b.Type, a.Identity, b.Identity, a.Size, b.Size, a.ModifiedNS, b.ModifiedNS)
		}
	}
	for _, b := range current.Entries {
		if _, exists := byName[b.Name]; exists {
			report("new entry %q: kind=%s; identity=%s; size=%d; mtime_ns=%d\n", b.Name, b.Type, b.Identity, b.Size, b.ModifiedNS)
		}
	}
	fmt.Fprintf(detail, "%d entries differ (at most 20 shown); values are those read by the preview and by this check.\n", count)
	if count == 0 {
		fmt.Fprintln(detail, "entries match, but the fingerprint format or digest differs; preview again.")
	}
	return true
}
