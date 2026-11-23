-- Derived-from link on materials.
--
-- A material may describe another material: a decision brief points at the
-- artifact version it summarizes. Keeping the pair explicit means "the brief of
-- the latest requirement" is a filter, not a heuristic, and a rejected-then-
-- resubmitted task cannot accumulate ambiguous briefs.
--
-- One brief per source version is enforced by the partial unique index.
ALTER TABLE materials ADD COLUMN source_material_id INTEGER REFERENCES materials(id);

CREATE UNIQUE INDEX ux_materials_source ON materials(source_material_id)
  WHERE source_material_id IS NOT NULL;
