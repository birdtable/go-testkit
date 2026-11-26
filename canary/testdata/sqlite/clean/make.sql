-- Made with: sqlite3 3.54.0 2026-04-09 12:25:13 8fa8248e303219400c646a885e36dfc52eae33d83f4412e3f369b2be5373aapl (64-bit)
-- From this folder: sqlite3 work.db < make.sql, then remove work.db, work.db-shm and work.db-wal.
-- The twin of ../leak with no planted value: the same rows and pages, the bodies plain text.
PRAGMA page_size = 1024;
PRAGMA journal_mode = WAL;
CREATE TABLE notes(name TEXT, body TEXT);
INSERT INTO notes VALUES ('row', 'nothing planted in this row, only text');
PRAGMA wal_checkpoint(TRUNCATE);
INSERT INTO notes VALUES ('wal', 'nothing planted in this page, only text');
.shell cp work.db app.db
.shell cp work.db-wal app.db-wal
