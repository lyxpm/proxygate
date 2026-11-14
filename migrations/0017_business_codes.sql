ALTER TABLE sites ADD COLUMN code TEXT;
ALTER TABLE applications ADD COLUMN code TEXT;
ALTER TABLE certificates ADD COLUMN code TEXT;
CREATE UNIQUE INDEX IF NOT EXISTS idx_sites_code ON sites(code);
CREATE UNIQUE INDEX IF NOT EXISTS idx_applications_code ON applications(code);
CREATE UNIQUE INDEX IF NOT EXISTS idx_certificates_code ON certificates(code);
