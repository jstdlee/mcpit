-- mcpit registry schema v1

CREATE TABLE sites (
  origin         TEXT PRIMARY KEY,
  active_version TEXT,
  state          TEXT NOT NULL DEFAULT 'listed',   -- listed | delisted | expired
  verdict        TEXT,                             -- good | bad | NULL
  expire_days    INTEGER,                          -- NULL = global setting
  source         TEXT NOT NULL DEFAULT 'community',-- community | owner | native | crawler
  delist_reason  TEXT,
  stars          INTEGER NOT NULL DEFAULT 0,
  calls          INTEGER NOT NULL DEFAULT 0,
  ok             INTEGER NOT NULL DEFAULT 0,
  fail           INTEGER NOT NULL DEFAULT 0,
  created_at     TEXT NOT NULL,
  updated_at     TEXT NOT NULL
);

CREATE TABLE versions (
  origin      TEXT NOT NULL,
  version     TEXT NOT NULL,
  hash        TEXT NOT NULL,
  pack        TEXT NOT NULL,
  state       TEXT NOT NULL,                       -- active | superseded | expired
  signature   TEXT NOT NULL,
  key_id      TEXT NOT NULL,
  from_submission TEXT,
  verified_at TEXT NOT NULL,
  created_at  TEXT NOT NULL,
  PRIMARY KEY (origin, version)
);
CREATE INDEX versions_hash ON versions(hash);

CREATE TABLE submissions (
  id         TEXT PRIMARY KEY,
  origin     TEXT NOT NULL,
  hash       TEXT NOT NULL,
  key_id     TEXT NOT NULL,
  pack       TEXT NOT NULL,
  state      TEXT NOT NULL,                        -- screening | done | rejected | quarantined
  outcome    TEXT,                                 -- confirmation | promoted | partial | alternative | rejected | quarantined
  reason     TEXT,
  diff       TEXT,
  tools      TEXT,                                 -- per-tool result JSON
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX submissions_hash ON submissions(hash);
CREATE INDEX submissions_key ON submissions(key_id, created_at);
CREATE INDEX submissions_origin ON submissions(origin, created_at);

CREATE TABLE agreements (
  hash       TEXT NOT NULL,
  key_id     TEXT NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY (hash, key_id)
);

CREATE TABLE keys (
  id          TEXT PRIMARY KEY,
  public_key  TEXT NOT NULL,
  name        TEXT,
  state       TEXT NOT NULL DEFAULT 'pending',     -- pending | approved | rejected | revoked
  reputation  INTEGER NOT NULL DEFAULT 0,
  note        TEXT,
  ip_hash     TEXT,
  created_at  TEXT NOT NULL,
  decided_at  TEXT
);

CREATE TABLE nonces (
  sig TEXT PRIMARY KEY,
  ts  INTEGER NOT NULL
);

CREATE TABLE decisions (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  submission_id TEXT,
  origin        TEXT,
  point         TEXT NOT NULL,
  subject       TEXT,
  model         TEXT NOT NULL,
  answer        TEXT,
  probs         TEXT,
  action        TEXT,
  created_at    TEXT NOT NULL
);
CREATE INDEX decisions_sub ON decisions(submission_id);

CREATE TABLE quarantine (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  submission_id TEXT NOT NULL,
  origin        TEXT NOT NULL,
  tool_id       TEXT NOT NULL,
  tool          TEXT NOT NULL,
  reason        TEXT NOT NULL,
  summary       TEXT,
  state         TEXT NOT NULL DEFAULT 'open',      -- open | approved | rejected
  created_at    TEXT NOT NULL,
  decided_at    TEXT
);

CREATE TABLE stars (
  origin     TEXT NOT NULL,
  key_id     TEXT NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY (origin, key_id)
);

CREATE TABLE counters_daily (
  origin TEXT NOT NULL,
  tool   TEXT NOT NULL,
  day    TEXT NOT NULL,
  ok     INTEGER NOT NULL DEFAULT 0,
  fail   INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (origin, tool, day)
);

CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE audit (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  actor      TEXT NOT NULL,
  action     TEXT NOT NULL,
  target     TEXT,
  detail     TEXT,
  created_at TEXT NOT NULL
);

INSERT INTO settings (key, value) VALUES
  ('expire_days', '60'),
  ('submit_per_key_per_hour', '20'),
  ('submit_per_site_per_hour', '30'),
  ('max_pack_bytes', '524288'),
  ('auto_promote', 'true'),
  ('unsure_low', '0.25'),
  ('unsure_high', '0.75'),
  ('verify_sample_percent', '5');
