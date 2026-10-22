-- +goose Up
-- Step 18b: sharing to users. A recipient with editor permission deletes
-- into the owner's Recycle Bin; deleted_by records who did it (SPEC 8.15).
ALTER TABLE trash_items ADD COLUMN deleted_by TEXT;

-- +goose Down
ALTER TABLE trash_items DROP COLUMN deleted_by;
