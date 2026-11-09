CREATE TABLE requests (
  id            TEXT PRIMARY KEY,
  received_at   INTEGER NOT NULL,
  method        TEXT NOT NULL,
  path          TEXT NOT NULL,
  query         TEXT NOT NULL DEFAULT '',
  headers       TEXT NOT NULL,
  body          BLOB NOT NULL,
  body_sha256   TEXT NOT NULL,
  seen_body     BLOB,
  provider      TEXT,
  event_type    TEXT
);
CREATE INDEX idx_requests_received ON requests(received_at DESC);

CREATE TABLE attempts (
  id               TEXT PRIMARY KEY,
  request_id       TEXT NOT NULL REFERENCES requests(id) ON DELETE CASCADE,
  kind             TEXT NOT NULL CHECK (kind IN ('forward', 'replay')),
  target_url       TEXT NOT NULL,
  resigned         INTEGER NOT NULL DEFAULT 0,
  started_at       INTEGER NOT NULL,
  duration_ms      INTEGER,
  status_code      INTEGER,
  response_body    BLOB,
  error            TEXT
);
CREATE INDEX idx_attempts_request ON attempts(request_id, started_at);

CREATE TABLE verifications (
  id          TEXT PRIMARY KEY,
  request_id  TEXT NOT NULL REFERENCES requests(id) ON DELETE CASCADE,
  provider    TEXT NOT NULL,
  ok          INTEGER NOT NULL,
  reason      TEXT NOT NULL,
  details     TEXT,
  checked_at  INTEGER NOT NULL
);
CREATE INDEX idx_verifications_request ON verifications(request_id, checked_at);
