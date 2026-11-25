-- Bodyweight: at most one entry per person per day. Logging a day again
-- replaces its entry. weight_kg is stored in kg like every weight; the person
-- types and reads it in their unit (users.weight_unit). note is optional free
-- text, NULL when not given. Nothing else changes: the table starts empty and
-- no existing row is read or written.
CREATE TABLE bodyweight (
  user_id INTEGER NOT NULL REFERENCES users(id),
  date TEXT NOT NULL CHECK (length(date) = 10),
  weight_kg REAL NOT NULL CHECK (weight_kg > 0),
  note TEXT CHECK (note IS NULL OR (note <> '' AND length(note) <= 500)),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  PRIMARY KEY (user_id, date)
);
