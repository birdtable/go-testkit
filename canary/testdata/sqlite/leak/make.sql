-- Made with: sqlite3 3.54.0 2026-04-09 12:25:13 8fa8248e303219400c646a885e36dfc52eae33d83f4412e3f369b2be5373aapl (64-bit)
-- From this folder: sqlite3 work.db < make.sql, then remove work.db, work.db-shm and work.db-wal.
-- app.db holds the first value in a row of its main file; app.db-wal holds the second in a page no
-- checkpoint has written back, copied while the database is still open, since sqlite3 checkpoints and
-- removes the -wal when it closes.
PRAGMA page_size = 1024;
PRAGMA journal_mode = WAL;
CREATE TABLE notes(name TEXT, body TEXT);
INSERT INTO notes VALUES ('row', 'tkcanary6c415da4838eade9e4aea23229d14752');
PRAGMA wal_checkpoint(TRUNCATE);
INSERT INTO notes VALUES ('wal', 'tkcanaryf584bf939d00240ebf0edc72e0a178c0');
.shell cp work.db app.db
.shell cp work.db-wal app.db-wal
