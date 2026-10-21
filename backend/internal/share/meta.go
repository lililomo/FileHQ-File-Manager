package share

import (
	"context"
	"fmt"

	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/listing"
)

// Annotate implements listing.MetaProvider: items that have a share get
// `shared` = public | users | both (SPEC §6.3 Item.shared; badge column).
// Disabled/expired/broken shares still count: the owner sees the badge
// until the share is deleted.
func (s *Service) Annotate(ctx context.Context, userID string, dir fsjail.VPath, items []*listing.Item) error {
	if len(items) == 0 {
		return nil
	}
	rows, err := s.repo.ListSharesInDir(ctx, userID, string(dir))
	if err != nil {
		return fmt.Errorf("share: annotate: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}
	modes := map[string]string{}
	for _, r := range rows {
		if prev := modes[r.Path]; prev == "" || prev == r.Mode {
			modes[r.Path] = r.Mode
		} else {
			modes[r.Path] = "both"
		}
	}
	for _, it := range items {
		if m, ok := modes[it.Path]; ok {
			it.Shared = m
		}
	}
	return nil
}

var _ listing.MetaProvider = (*Service)(nil)
