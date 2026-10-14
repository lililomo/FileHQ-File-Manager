package files

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"kppbwi.app/filemanager/internal/db"
)

// ReferenceUpdater keeps one path-keyed table in step with rename/move
// (SPEC §5.7). The built-in tables (favorites, pins, file_notes, file_tags,
// file_versions) are registered by this package; shares registers its own
// in Step 18a. tx is the repository bound to the operation's transaction.
type ReferenceUpdater interface {
	// Rewrite moves oldPath and everything below it to newPath.
	Rewrite(ctx context.Context, tx db.Repository, userID, oldPath, newPath string) error
	// Delete removes rows for path and everything below it.
	Delete(ctx context.Context, tx db.Repository, userID, path string) error
}

var (
	refMu   sync.RWMutex
	refRegs = map[string]ReferenceUpdater{}
)

// RegisterReferenceUpdater adds (or replaces) the updater for name.
func RegisterReferenceUpdater(name string, u ReferenceUpdater) {
	refMu.Lock()
	defer refMu.Unlock()
	refRegs[name] = u
}

// ReferenceUpdaters returns the registered updaters in name order.
func ReferenceUpdaters() []ReferenceUpdater {
	refMu.RLock()
	defer refMu.RUnlock()
	names := make([]string, 0, len(refRegs))
	for n := range refRegs {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]ReferenceUpdater, 0, len(names))
	for _, n := range names {
		out = append(out, refRegs[n])
	}
	return out
}

type tableUpdater struct{ table db.RefTable }

func (t tableUpdater) Rewrite(ctx context.Context, tx db.Repository, userID, oldPath, newPath string) error {
	_, err := tx.RewriteRefs(ctx, t.table, userID, oldPath, newPath)
	return err
}

func (t tableUpdater) Delete(ctx context.Context, tx db.Repository, userID, path string) error {
	_, err := tx.DeleteRefs(ctx, t.table, userID, path)
	return err
}

func init() {
	for _, t := range db.RefTables {
		RegisterReferenceUpdater(string(t), tableUpdater{table: t})
	}
}

// UpdateReferences rewrites every registered table from oldPath to newPath
// inside tx: rows already at the destination are removed first (an
// overwritten item's stale metadata must not survive), then the source
// rows move. trash_items.original_path and activity_logs are never touched.
func UpdateReferences(ctx context.Context, tx db.Repository, userID, oldPath, newPath string) error {
	if oldPath == newPath {
		return nil
	}
	for _, u := range ReferenceUpdaters() {
		if err := u.Delete(ctx, tx, userID, newPath); err != nil {
			return fmt.Errorf("files: clear references at %s: %w", newPath, err)
		}
		if err := u.Rewrite(ctx, tx, userID, oldPath, newPath); err != nil {
			return fmt.Errorf("files: rewrite references %s -> %s: %w", oldPath, newPath, err)
		}
	}
	return nil
}

// DeleteReferences removes rows for path and its subtree from every
// registered table (item overwritten away, merged folder removed,
// permanent delete).
func DeleteReferences(ctx context.Context, tx db.Repository, userID, path string) error {
	for _, u := range ReferenceUpdaters() {
		if err := u.Delete(ctx, tx, userID, path); err != nil {
			return fmt.Errorf("files: delete references %s: %w", path, err)
		}
	}
	return nil
}
