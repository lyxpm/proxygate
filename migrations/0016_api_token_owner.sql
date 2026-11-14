ALTER TABLE api_tokens ADD COLUMN user_id INTEGER;
CREATE INDEX IF NOT EXISTS idx_api_tokens_user ON api_tokens(user_id);
