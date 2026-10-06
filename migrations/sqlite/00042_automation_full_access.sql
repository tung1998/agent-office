-- +goose Up
-- ADR-074: automation can override its agent's administrator permissions and
-- extra read-only directories (agent.permissions already carries them as JSON)
ALTER TABLE automations ADD COLUMN permission_mode TEXT NOT NULL DEFAULT 'agent';
ALTER TABLE automations ADD COLUMN override_full_access BOOLEAN NOT NULL DEFAULT 0;
ALTER TABLE automations ADD COLUMN override_admin_by TEXT NOT NULL DEFAULT '';
ALTER TABLE automations ADD COLUMN override_extra_dirs TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(override_extra_dirs));

-- +goose Down
ALTER TABLE automations DROP COLUMN permission_mode;
ALTER TABLE automations DROP COLUMN override_full_access;
ALTER TABLE automations DROP COLUMN override_admin_by;
ALTER TABLE automations DROP COLUMN override_extra_dirs;
