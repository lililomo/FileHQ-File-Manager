-- +goose Up
-- Step 19b: admin task and share listings across users (SPEC 6.3 admin/tasks,
-- admin/shares) are ordered by (created_at, id) without a user filter.
CREATE INDEX tasks_created_idx ON tasks (created_at DESC, id DESC);
CREATE INDEX shares_created_idx ON shares (created_at DESC, id DESC);

-- +goose Down
DROP INDEX shares_created_idx;
DROP INDEX tasks_created_idx;
