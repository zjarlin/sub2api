\set ON_ERROR_STOP on
BEGIN;
CREATE TEMP TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()) ON COMMIT DROP;
INSERT INTO settings(key,value) VALUES ('model_aliases', '{"groups":[{"canonical":"deepseek-v4.1-flash","aliases":["deepseek-flash","cline-pass/deepseek-v4.1-flash","deepseek/deepseek-v4.1-flash","deepseek/deepseek-v4.1-flash-1017"]},{"canonical":"custom","aliases":["custom/old"]}]}'),('other','retained');
\ir ../275_deepseek_model_routing_tree.sql
DO $$
DECLARE p JSONB := (SELECT value::jsonb FROM settings WHERE key='model_aliases');
BEGIN
 IF p->'groups'->0->>'canonical' <> 'ds' THEN RAISE EXCEPTION 'missing root'; END IF;
 IF p->'groups'->1->>'canonical' <> 'deepseek-flash' OR p->'groups'->1->>'parent' <> 'ds' THEN RAISE EXCEPTION 'missing child'; END IF;
 IF NOT (p->'groups'->1->'aliases' ? 'deepseek-v4.1-flash') THEN RAISE EXCEPTION 'old ID must remain accepted'; END IF;
 IF p->'groups'->1->'routes'->0->>'model' <> 'deepseek/deepseek-v4.1-flash-1017' THEN RAISE EXCEPTION '1017 must be first'; END IF;
 IF p->'groups'->1->'routes'->0->'base_urls'->>0 <> 'https://api.cline.bot/api' THEN RAISE EXCEPTION 'base URL missing'; END IF;
 IF p->'groups'->2 <> '{"canonical":"custom","aliases":["custom/old"]}'::jsonb THEN RAISE EXCEPTION 'custom group changed'; END IF;
END $$;
CREATE TEMP TABLE after_first ON COMMIT DROP AS SELECT * FROM settings;
\ir ../275_deepseek_model_routing_tree.sql
DO $$ BEGIN IF EXISTS(SELECT * FROM settings EXCEPT SELECT * FROM after_first) THEN RAISE EXCEPTION 'not idempotent'; END IF; END $$;
UPDATE settings SET value='{"groups":[]}' WHERE key='model_aliases';
\ir ../275_deepseek_model_routing_tree.sql
DO $$ BEGIN IF (SELECT value::jsonb FROM settings WHERE key='model_aliases') <> '{"groups":[]}'::jsonb THEN RAISE EXCEPTION 'empty policy changed'; END IF; END $$;
UPDATE settings SET value='{"groups":[{"canonical":"deepseek-v4.1-flash","aliases":["deepseek-flash"]},{"canonical":"custom","aliases":["ds"]}]}' WHERE key='model_aliases';
CREATE TEMP TABLE conflict_before ON COMMIT DROP AS SELECT * FROM settings;
\ir ../275_deepseek_model_routing_tree.sql
DO $$ BEGIN IF EXISTS(SELECT * FROM settings EXCEPT SELECT * FROM conflict_before) THEN RAISE EXCEPTION 'custom ownership overwritten'; END IF; END $$;
ROLLBACK;
