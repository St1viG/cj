-- +goose Up
-- A PENDING instance that is terminated ends TERMINATED without ever being
-- placed (roadmap P3.6), so TERMINATED may have no node too.
ALTER TABLE instances DROP CONSTRAINT instances_check;
ALTER TABLE instances ADD CONSTRAINT instances_unplaced_phase
  CHECK (node_id IS NOT NULL OR phase IN ('pending', 'failed', 'terminated'));

-- +goose Down
ALTER TABLE instances DROP CONSTRAINT instances_unplaced_phase;
ALTER TABLE instances ADD CONSTRAINT instances_check
  CHECK (node_id IS NOT NULL OR phase IN ('pending', 'failed'));
