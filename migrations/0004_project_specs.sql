-- Preserve the old spec and its entire chain as the first independent document.
CREATE TABLE project_specs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  spec_id TEXT NOT NULL UNIQUE,
  project_id INTEGER NOT NULL REFERENCES projects(id),
  seq_no INTEGER NOT NULL,
  material_id INTEGER NOT NULL REFERENCES materials(id),
  create_time TEXT NOT NULL,
  update_time TEXT NOT NULL,
  UNIQUE(project_id, seq_no)
);

INSERT INTO project_specs(spec_id, project_id, seq_no, material_id, create_time, update_time)
SELECT p.project_id || '-S0001', p.id, 1, p.spec_material_id, m.create_time, m.update_time
FROM projects p JOIN materials m ON m.id = p.spec_material_id;
