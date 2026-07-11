// Package sqlite implements the persistent metadata and task store using SQLite.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const staleRunningAfter = 2 * time.Minute

const initialMigration = `
CREATE TABLE sync_roots (id INTEGER PRIMARY KEY AUTOINCREMENT, local_path TEXT NOT NULL, canonical_path TEXT NOT NULL UNIQUE, remote_path TEXT NOT NULL, remote_docid TEXT, enabled INTEGER NOT NULL DEFAULT 1, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
CREATE TABLE files (id INTEGER PRIMARY KEY AUTOINCREMENT, sync_root_id INTEGER NOT NULL, local_path TEXT NOT NULL, canonical_path TEXT NOT NULL, remote_path TEXT NOT NULL, remote_docid TEXT, size INTEGER NOT NULL DEFAULT 0, mod_time INTEGER NOT NULL DEFAULT 0, is_dir INTEGER NOT NULL DEFAULT 0, etag TEXT, sync_status TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, UNIQUE(sync_root_id,canonical_path), FOREIGN KEY(sync_root_id) REFERENCES sync_roots(id) ON DELETE CASCADE);
CREATE TABLE tasks (id INTEGER PRIMARY KEY AUTOINCREMENT, operation TEXT NOT NULL CHECK(operation IN ('upload','download','mkdir','remove','rename')), local_path TEXT, canonical_path TEXT NOT NULL, remote_docid TEXT, destination TEXT, status TEXT NOT NULL CHECK(status IN ('pending','running','retry_wait','succeeded','failed')), attempts INTEGER NOT NULL DEFAULT 0, next_retry_at INTEGER, last_error TEXT, claimed_at INTEGER, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
CREATE INDEX idx_tasks_ready ON tasks(status,next_retry_at,created_at);
CREATE INDEX idx_tasks_canonical_path ON tasks(canonical_path,status);
CREATE UNIQUE INDEX idx_tasks_one_running_per_path ON tasks(canonical_path) WHERE status='running';
CREATE UNIQUE INDEX idx_tasks_active_dedup ON tasks(operation,canonical_path,COALESCE(destination,'')) WHERE status IN ('pending','running','retry_wait');
CREATE TABLE conflicts (id INTEGER PRIMARY KEY AUTOINCREMENT, local_path TEXT NOT NULL, remote_path TEXT NOT NULL, local_etag TEXT, remote_etag TEXT, resolution TEXT NOT NULL DEFAULT 'unresolved', created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at INTEGER NOT NULL);
`

const taskLifecycleMigration = `
ALTER TABLE tasks RENAME TO tasks_v1;
CREATE TABLE tasks (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 operation TEXT NOT NULL CHECK(operation IN ('upload','download','mkdir','remove','rename')),
 sync_root_id INTEGER NOT NULL DEFAULT 0,
 local_path TEXT,
 canonical_path TEXT NOT NULL,
 canonical_remote_path TEXT NOT NULL,
 remote_docid TEXT,
 destination TEXT,
 status TEXT NOT NULL CHECK(status IN ('pending','running','retry_wait','blocked_auth','succeeded','failed','cancelled','needs_reconcile')),
 attempts INTEGER NOT NULL DEFAULT 0,
 next_retry_at INTEGER,
 last_error TEXT,
 error_class TEXT,
 claimed_at INTEGER,
 dirty_after_run INTEGER NOT NULL DEFAULT 0,
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL,
 completed_at INTEGER
);
INSERT INTO tasks(id,operation,local_path,canonical_path,canonical_remote_path,remote_docid,destination,status,attempts,next_retry_at,last_error,claimed_at,created_at,updated_at)
 SELECT id,operation,local_path,canonical_path,
 CASE WHEN destination IS NULL OR trim(destination)='' THEN canonical_path ELSE destination END,
 remote_docid,destination,status,attempts,next_retry_at,last_error,claimed_at,created_at,updated_at FROM tasks_v1;
UPDATE tasks SET sync_root_id=COALESCE((SELECT id FROM sync_roots
 WHERE tasks.canonical_path=sync_roots.canonical_path OR tasks.canonical_path LIKE sync_roots.canonical_path||'\%'
 ORDER BY length(sync_roots.canonical_path) DESC LIMIT 1),0);
UPDATE tasks SET completed_at=updated_at WHERE status IN ('succeeded','failed');
DROP TABLE tasks_v1;
CREATE INDEX idx_tasks_ready ON tasks(status,next_retry_at,created_at);
CREATE INDEX idx_tasks_canonical_path ON tasks(canonical_path,status);
CREATE UNIQUE INDEX idx_tasks_one_running_per_path ON tasks(sync_root_id,canonical_remote_path) WHERE status='running';
CREATE UNIQUE INDEX idx_tasks_active_dedup ON tasks(sync_root_id,operation,canonical_remote_path) WHERE status IN ('pending','running','retry_wait','blocked_auth','needs_reconcile');
`

type Store struct {
	db   *sql.DB
	path string
}

func Open(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("sqlite open: empty directory")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("sqlite open: %w", err)
	}
	dbPath := filepath.Join(dir, "metadata.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("sqlite open: %w", err)
	}
	// A single connection gives deterministic write serialization. Worker
	// concurrency remains above the Store while SQLite transactions stay atomic.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &Store{db: db, path: dbPath}
	if err := s.configure(); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite migrate: %w", err)
	}
	if _, err := s.RecoverStaleTasks(time.Now().Add(-staleRunningAfter)); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite recover: %w", err)
	}
	return s, nil
}

func (s *Store) configure() error {
	for _, q := range []string{"PRAGMA foreign_keys=ON", "PRAGMA journal_mode=WAL", "PRAGMA synchronous=NORMAL", "PRAGMA busy_timeout=5000"} {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("sqlite configure: %w", err)
		}
	}
	return nil
}

func (s *Store) migrate() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations(version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		return err
	}
	for _, migration := range []struct {
		version int
		script  string
	}{{1, initialMigration}, {2, taskLifecycleMigration}} {
		version, script := migration.version, migration.script
		var applied int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version=?", version).Scan(&applied); err != nil {
			return err
		}
		if applied != 0 {
			continue
		}
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err = tx.Exec(script); err == nil {
			_, err = tx.Exec("INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)", version, Now())
		}
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", version, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }
func (s *Store) Path() string { return s.path }
func Now() int64              { return time.Now().Unix() }

func canonicalLocal(p string) string {
	if p == "" {
		return ""
	}
	abs, err := filepath.Abs(filepath.Clean(p))
	if err != nil {
		abs = filepath.Clean(p)
	}
	return strings.ToLower(abs)
}

func canonicalRemote(p string) string {
	p = strings.ReplaceAll(strings.TrimSpace(p), `\`, "/")
	if p == "" || p == "/" {
		return "/"
	}
	return path.Clean("/" + strings.TrimLeft(p, "/"))
}

func nullable(v string) any {
	if v == "" {
		return nil
	}
	return v
}

type SyncRootRow struct {
	ID         int64
	LocalPath  string
	RemotePath string
	Enabled    bool
	CreatedAt  int64
}

func (s *Store) InsertSyncRoot(localPath, remotePath string) (int64, error) {
	now := Now()
	r, err := s.db.Exec(`INSERT INTO sync_roots(local_path,canonical_path,remote_path,enabled,created_at,updated_at) VALUES(?,?,?,1,?,?)`, localPath, canonicalLocal(localPath), canonicalRemote(remotePath), now, now)
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

func (s *Store) ListSyncRoots() ([]SyncRootRow, error) {
	rows, err := s.db.Query("SELECT id,local_path,remote_path,enabled,created_at FROM sync_roots ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SyncRootRow
	for rows.Next() {
		var r SyncRootRow
		if err := rows.Scan(&r.ID, &r.LocalPath, &r.RemotePath, &r.Enabled, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) GetSyncRoot(id int64) (*SyncRootRow, error) {
	var r SyncRootRow
	err := s.db.QueryRow("SELECT id,local_path,remote_path,enabled,created_at FROM sync_roots WHERE id=?", id).Scan(&r.ID, &r.LocalPath, &r.RemotePath, &r.Enabled, &r.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("sync_root %d not found", id)
	}
	return &r, err
}

func (s *Store) UpdateSyncRootEnabled(id int64, enabled bool) error {
	r, err := s.db.Exec("UPDATE sync_roots SET enabled=?,updated_at=? WHERE id=?", enabled, Now(), id)
	return requireAffected(r, err)
}

func (s *Store) DeleteSyncRoot(id int64) error {
	_, err := s.db.Exec("DELETE FROM sync_roots WHERE id=?", id)
	return err
}

type FileRow struct {
	ID         int64
	LocalPath  string
	RemotePath string
	Size       int64
	ModTime    int64
	IsDir      bool
	ETag       string
	SyncStatus string
	SyncRootID int64
	CreatedAt  int64
	UpdatedAt  int64
}

func (s *Store) UpsertFile(f *FileRow) (int64, error) {
	now := Now()
	var id int64
	err := s.db.QueryRow(`INSERT INTO files(sync_root_id,local_path,canonical_path,remote_path,size,mod_time,is_dir,etag,sync_status,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(sync_root_id,canonical_path) DO UPDATE SET
		local_path=excluded.local_path,remote_path=excluded.remote_path,size=excluded.size,mod_time=excluded.mod_time,
		is_dir=excluded.is_dir,etag=excluded.etag,sync_status=excluded.sync_status,updated_at=excluded.updated_at RETURNING id`,
		f.SyncRootID, f.LocalPath, canonicalLocal(f.LocalPath), canonicalRemote(f.RemotePath), f.Size, f.ModTime, f.IsDir, f.ETag, f.SyncStatus, now, now).Scan(&id)
	return id, err
}

const fileColumns = "id,local_path,remote_path,size,mod_time,is_dir,etag,sync_status,sync_root_id,created_at,updated_at"

func scanFiles(rows *sql.Rows) ([]FileRow, error) {
	defer rows.Close()
	var out []FileRow
	for rows.Next() {
		var f FileRow
		if err := rows.Scan(&f.ID, &f.LocalPath, &f.RemotePath, &f.Size, &f.ModTime, &f.IsDir, &f.ETag, &f.SyncStatus, &f.SyncRootID, &f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) ListFilesByRoot(id int64) ([]FileRow, error) {
	rows, err := s.db.Query("SELECT "+fileColumns+" FROM files WHERE sync_root_id=? ORDER BY id", id)
	if err != nil {
		return nil, err
	}
	return scanFiles(rows)
}

func (s *Store) ListFilesByStatus(status string) ([]FileRow, error) {
	rows, err := s.db.Query("SELECT "+fileColumns+" FROM files WHERE sync_status=? ORDER BY id", status)
	if err != nil {
		return nil, err
	}
	return scanFiles(rows)
}

func (s *Store) DeleteFile(id int64) error {
	_, err := s.db.Exec("DELETE FROM files WHERE id=?", id)
	return err
}
func (s *Store) DeleteFilesByRoot(id int64) error {
	_, err := s.db.Exec("DELETE FROM files WHERE sync_root_id=?", id)
	return err
}

type TaskRow struct {
	ID            int64
	TaskType      string
	Operation     string
	LocalPath     *string
	RemotePath    *string
	RemoteDocID   *string
	Destination   *string
	State         string
	Status        string
	RetryCount    int64
	Attempts      int64
	NextRetryAt   *int64
	LastError     *string
	CreatedAt     int64
	UpdatedAt     int64
	SyncRootID    int64
	ErrorClass    *string
	CompletedAt   *int64
	DirtyAfterRun bool
}

type TaskState string

const (
	TaskPending        TaskState = "pending"
	TaskRunning        TaskState = "running"
	TaskRetryWait      TaskState = "retry_wait"
	TaskBlockedAuth    TaskState = "blocked_auth"
	TaskSucceeded      TaskState = "succeeded"
	TaskFailed         TaskState = "failed"
	TaskCancelled      TaskState = "cancelled"
	TaskNeedsReconcile TaskState = "needs_reconcile"
)

type TaskQuery struct {
	States          []TaskState
	Limit           int
	IncludeTerminal bool
	Operation       string
}

const MaxTaskQueryLimit = 500

func normalizeOperation(op string) string {
	if op == "delete" {
		return "remove"
	}
	return op
}
func legacyOperation(op string) string {
	if op == "remove" {
		return "delete"
	}
	return op
}

func (s *Store) InsertTask(operation, localPath, destination string) (int64, error) {
	return s.EnqueueOrMerge(context.Background(), 0, operation, localPath, destination)
}

func (s *Store) EnqueueOrMerge(ctx context.Context, syncRootID int64, operation, localPath, destination string) (int64, error) {
	op := normalizeOperation(operation)
	switch op {
	case "upload", "download", "mkdir", "remove", "rename":
	default:
		return 0, fmt.Errorf("sqlite: invalid operation %q", operation)
	}
	canonical := canonicalLocal(localPath)
	remoteCanonical := canonicalRemote(destination)
	if canonical == "" {
		canonical = remoteCanonical
	}
	now := Now()
	var id int64
	err := s.db.QueryRowContext(ctx, `INSERT INTO tasks(operation,sync_root_id,local_path,canonical_path,canonical_remote_path,destination,status,attempts,created_at,updated_at)
	 VALUES(?,?,?,?,?,?,'pending',0,?,?)
	 ON CONFLICT(sync_root_id,operation,canonical_remote_path) WHERE status IN ('pending','running','retry_wait','blocked_auth','needs_reconcile')
	 DO UPDATE SET local_path=excluded.local_path,canonical_path=excluded.canonical_path,updated_at=excluded.updated_at,
	 dirty_after_run=CASE WHEN tasks.status='running' THEN 1 ELSE tasks.dirty_after_run END RETURNING id`,
		op, syncRootID, nullable(localPath), canonical, remoteCanonical, nullable(destination), now, now).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("sqlite insert task: %w", err)
	}
	return id, nil
}

type rowScanner interface{ Scan(...any) error }

func scanTask(row rowScanner) (TaskRow, error) {
	var t TaskRow
	var local, docID, destination, last sql.NullString
	var next, completed sql.NullInt64
	var errorClass sql.NullString
	if err := row.Scan(&t.ID, &t.SyncRootID, &t.Operation, &local, &docID, &destination, &t.Status, &t.Attempts, &next, &errorClass, &last, &t.DirtyAfterRun, &t.CreatedAt, &t.UpdatedAt, &completed); err != nil {
		return t, err
	}
	t.TaskType, t.State, t.RetryCount = legacyOperation(t.Operation), t.Status, t.Attempts
	if local.Valid {
		v := local.String
		t.LocalPath = &v
	}
	if docID.Valid {
		v := docID.String
		t.RemoteDocID = &v
	}
	if destination.Valid {
		v := destination.String
		t.Destination, t.RemotePath = &v, &v
	}
	if next.Valid {
		v := next.Int64
		t.NextRetryAt = &v
	}
	if last.Valid {
		v := last.String
		t.LastError = &v
	}
	if errorClass.Valid {
		v := errorClass.String
		t.ErrorClass = &v
	}
	if completed.Valid {
		v := completed.Int64
		t.CompletedAt = &v
	}
	return t, nil
}

const taskSelect = `SELECT id,sync_root_id,operation,local_path,remote_docid,destination,status,attempts,next_retry_at,error_class,last_error,dirty_after_run,created_at,updated_at,completed_at FROM tasks`

func (s *Store) ListTasks(ctx context.Context, q TaskQuery) ([]TaskRow, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > MaxTaskQueryLimit {
		limit = MaxTaskQueryLimit
	}
	where, args := []string{"1=1"}, []any{}
	if q.Operation != "" {
		where = append(where, "operation=?")
		args = append(args, normalizeOperation(q.Operation))
	}
	if len(q.States) > 0 {
		marks := make([]string, len(q.States))
		for i, v := range q.States {
			marks[i] = "?"
			args = append(args, string(v))
		}
		where = append(where, "status IN ("+strings.Join(marks, ",")+")")
	} else if !q.IncludeTerminal {
		where = append(where, "status IN ('pending','running','retry_wait','blocked_auth','needs_reconcile')")
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, taskSelect+" WHERE "+strings.Join(where, " AND ")+" ORDER BY updated_at DESC,id DESC LIMIT ?", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TaskRow
	for rows.Next() {
		t, e := scanTask(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) ListPendingTasks(operation string, limit int) ([]TaskRow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(taskSelect+` WHERE operation=? AND (status='pending' OR (status='retry_wait' AND next_retry_at<=?)) ORDER BY created_at,id LIMIT ?`, normalizeOperation(operation), Now(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TaskRow
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) ClaimTask(id int64) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	now := Now()
	r, err := tx.Exec(`UPDATE tasks AS target SET status='running',attempts=attempts+1,claimed_at=?,updated_at=?,next_retry_at=NULL
		WHERE id=? AND (status='pending' OR (status='retry_wait' AND next_retry_at<=?))
		AND NOT EXISTS(SELECT 1 FROM tasks running WHERE running.status='running' AND running.id<>target.id AND
		(running.canonical_path=target.canonical_path OR (running.sync_root_id=target.sync_root_id AND running.canonical_remote_path=target.canonical_remote_path)))`, now, now, id, now)
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return n == 1, nil
}

func (s *Store) UpdateTaskState(id int64, status string, attempts int64, next *int64, last *string) error {
	if status == "failed" && next != nil {
		status = "retry_wait"
	}
	completed := any(nil)
	if status == "succeeded" || status == "failed" || status == "cancelled" {
		completed = Now()
	}
	r, err := s.db.Exec("UPDATE tasks SET status=?,attempts=?,next_retry_at=?,last_error=?,updated_at=?,completed_at=? WHERE id=?", status, attempts, next, last, Now(), completed, id)
	return requireAffected(r, err)
}

func requireAffected(r sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) DeleteTask(id int64) error {
	_, err := s.db.Exec("DELETE FROM tasks WHERE id=?", id)
	return err
}
func (s *Store) MarkTaskDead(id int64) error {
	_, err := s.db.Exec("UPDATE tasks SET status='failed',updated_at=? WHERE id=?", Now(), id)
	return err
}
func (s *Store) CompleteTask(id int64) error {
	now := Now()
	return requireAffected(s.db.Exec("UPDATE tasks SET status='succeeded',completed_at=?,updated_at=?,next_retry_at=NULL,last_error=NULL,error_class=NULL,claimed_at=NULL WHERE id=? AND status='running'", now, now, id))
}

func (s *Store) MarkFailed(id int64, class, message string) error {
	now := Now()
	return requireAffected(s.db.Exec("UPDATE tasks SET status='failed',completed_at=?,updated_at=?,next_retry_at=NULL,error_class=?,last_error=?,claimed_at=NULL WHERE id=? AND status='running'", now, now, nullable(class), nullable(message), id))
}
func (s *Store) MarkRetry(id int64, attempts, next int64, class, message string) error {
	return requireAffected(s.db.Exec("UPDATE tasks SET status='retry_wait',attempts=?,next_retry_at=?,updated_at=?,error_class=?,last_error=?,claimed_at=NULL WHERE id=? AND status='running'", attempts, next, Now(), nullable(class), nullable(message), id))
}
func (s *Store) MarkCancelled(id int64, class, message string) error {
	now := Now()
	return requireAffected(s.db.Exec("UPDATE tasks SET status='cancelled',completed_at=?,updated_at=?,next_retry_at=NULL,error_class=?,last_error=?,claimed_at=NULL WHERE id=? AND status IN ('pending','running','retry_wait')", now, now, nullable(class), nullable(message), id))
}
func (s *Store) MarkBlockedAuth(id int64, message string) error {
	return requireAffected(s.db.Exec("UPDATE tasks SET status='blocked_auth',updated_at=?,next_retry_at=NULL,error_class='session_expired',last_error=?,claimed_at=NULL WHERE id=? AND status='running'", Now(), nullable(message), id))
}
func (s *Store) MarkNeedsReconcile(id int64, message string) error {
	return requireAffected(s.db.Exec("UPDATE tasks SET status='needs_reconcile',updated_at=?,next_retry_at=NULL,error_class='store_commit_failed',last_error=?,claimed_at=NULL WHERE id=?", Now(), nullable(message), id))
}
func (s *Store) CancelActiveByLocalPath(ctx context.Context, localPath, class string) (int64, error) {
	now := Now()
	r, e := s.db.ExecContext(ctx, "UPDATE tasks SET status='cancelled',completed_at=?,updated_at=?,next_retry_at=NULL,error_class=?,last_error=?,claimed_at=NULL WHERE canonical_path=? AND status IN ('pending','retry_wait')", now, now, class, class, canonicalLocal(localPath))
	if e != nil {
		return 0, e
	}
	return r.RowsAffected()
}
func (s *Store) ResumeBlockedAuth() (int64, error) {
	r, e := s.db.Exec("UPDATE tasks SET status='pending',error_class=NULL,last_error=NULL,updated_at=? WHERE status='blocked_auth'", Now())
	if e != nil {
		return 0, e
	}
	return r.RowsAffected()
}

func (s *Store) RecoverStaleTasks(before time.Time) (int64, error) {
	r, err := s.db.Exec("UPDATE tasks SET status='pending',claimed_at=NULL,updated_at=? WHERE status='running' AND claimed_at<?", Now(), before.Unix())
	if err != nil {
		return 0, err
	}
	return r.RowsAffected()
}

func (s *Store) CompleteTaskWithFile(id int64, f *FileRow) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := Now()
	if f != nil {
		_, err = tx.Exec(`INSERT INTO files(sync_root_id,local_path,canonical_path,remote_path,size,mod_time,is_dir,etag,sync_status,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(sync_root_id,canonical_path) DO UPDATE SET remote_path=excluded.remote_path,size=excluded.size,mod_time=excluded.mod_time,is_dir=excluded.is_dir,etag=excluded.etag,sync_status=excluded.sync_status,updated_at=excluded.updated_at`, f.SyncRootID, f.LocalPath, canonicalLocal(f.LocalPath), canonicalRemote(f.RemotePath), f.Size, f.ModTime, f.IsDir, f.ETag, f.SyncStatus, now, now)
		if err != nil {
			return err
		}
	}
	r, err := tx.Exec("UPDATE tasks SET status='succeeded',completed_at=?,updated_at=?,next_retry_at=NULL,last_error=NULL,error_class=NULL,claimed_at=NULL WHERE id=? AND status='running'", now, now, id)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("complete task %d: not running", id)
	}
	return tx.Commit()
}

type ConflictRow struct {
	ID                                                       int64
	LocalPath, RemotePath, LocalETag, RemoteETag, Resolution string
	CreatedAt, UpdatedAt                                     int64
}

func (s *Store) InsertConflict(lp, rp, le, re string) (int64, error) {
	now := Now()
	r, err := s.db.Exec("INSERT INTO conflicts(local_path,remote_path,local_etag,remote_etag,resolution,created_at,updated_at) VALUES(?,?,?,?,'unresolved',?,?)", lp, rp, le, re, now, now)
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}
func (s *Store) ListConflicts() ([]ConflictRow, error) {
	rows, err := s.db.Query("SELECT id,local_path,remote_path,local_etag,remote_etag,resolution,created_at,updated_at FROM conflicts WHERE resolution='unresolved' ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConflictRow
	for rows.Next() {
		var c ConflictRow
		if err := rows.Scan(&c.ID, &c.LocalPath, &c.RemotePath, &c.LocalETag, &c.RemoteETag, &c.Resolution, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (s *Store) ResolveConflict(id int64, resolution string) error {
	_, err := s.db.Exec("UPDATE conflicts SET resolution=?,updated_at=? WHERE id=?", resolution, Now(), id)
	return err
}

func (s *Store) GetSetting(key string) (string, error) {
	var value string
	err := s.db.QueryRow("SELECT value FROM settings WHERE key=?", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("setting %q not found", key)
	}
	return value, err
}
func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec("INSERT INTO settings(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at", key, value, Now())
	return err
}
func (s *Store) DeleteSetting(key string) error {
	_, err := s.db.Exec("DELETE FROM settings WHERE key=?", key)
	return err
}
