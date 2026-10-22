-- +goose Up
-- Step 20 (review E m-08): the admin audit filter by action walked the
-- (at, id) index; this index serves action-prefixed pages in order.
CREATE INDEX IF NOT EXISTS idx_audit_logs_action_at ON audit_logs(action, at DESC, id DESC);

-- +goose Down
DROP INDEX IF EXISTS idx_audit_logs_action_at;
