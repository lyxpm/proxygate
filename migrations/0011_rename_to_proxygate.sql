ALTER TABLE upstreams RENAME TO applications;
ALTER TABLE upstream_nodes RENAME TO application_nodes;
ALTER TABLE application_nodes RENAME COLUMN upstream_id TO application_id;
ALTER TABLE sites RENAME COLUMN upstream_id TO application_id;
