package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/polyxmedia/mnemos/internal/native"
)

// Native returns the native-note index backed by this DB.
func (d *DB) Native() native.Store { return &nativeStore{db: d.sql} }

type nativeStore struct{ db *sql.DB }

// nameWeight and descriptionWeight make a note whose name or description
// matches outrank one that merely mentions the word in its body. Name and
// description are what a note's author wrote to say what it is about.
const (
	nameWeight        = 6.0
	descriptionWeight = 4.0
	bodyWeight        = 1.0
)

func (s *nativeStore) FileStates(ctx context.Context, root string) (map[string]native.FileState, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT path, size, modified_at FROM native_docs WHERE root = ?`, root)
	if err != nil {
		return nil, fmt.Errorf("native file states: %w", err)
	}
	defer rows.Close()

	out := map[string]native.FileState{}
	for rows.Next() {
		var path string
		var state native.FileState
		if err := rows.Scan(&path, &state.Size, &state.ModifiedAt); err != nil {
			return nil, fmt.Errorf("scan native file state: %w", err)
		}
		state.ModifiedAt = state.ModifiedAt.UTC()
		out[path] = state
	}
	return out, rows.Err()
}

func (s *nativeStore) Apply(ctx context.Context, docs []native.Doc, removed []string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("native apply: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, path := range removed {
		if _, err := tx.ExecContext(ctx, `DELETE FROM native_docs WHERE path = ?`, path); err != nil {
			return fmt.Errorf("native remove %s: %w", path, err)
		}
	}
	for _, d := range docs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO native_docs
				(id, path, root, project, name, description, doc_type, body, size, modified_at, indexed_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(path) DO UPDATE SET
				root=excluded.root, project=excluded.project, name=excluded.name,
				description=excluded.description, doc_type=excluded.doc_type, body=excluded.body,
				size=excluded.size, modified_at=excluded.modified_at, indexed_at=excluded.indexed_at`,
			d.ID, d.Path, d.Root, d.Project, d.Name, d.Description, d.DocType, d.Body,
			d.Size, d.ModifiedAt.UTC(), now.UTC()); err != nil {
			return fmt.Errorf("native upsert %s: %w", d.Path, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("native apply: commit: %w", err)
	}
	return nil
}

const nativeColumns = `d.id, d.path, d.root, d.project, d.name, d.description, d.doc_type, d.body, d.size, d.modified_at`

func scanNativeDoc(dest *native.Doc) []any {
	return []any{
		&dest.ID, &dest.Path, &dest.Root, &dest.Project, &dest.Name,
		&dest.Description, &dest.DocType, &dest.Body, &dest.Size, &dest.ModifiedAt,
	}
}

func (s *nativeStore) Search(ctx context.Context, query string, roots []string, limit int) ([]native.Hit, error) {
	if len(roots) == 0 {
		return nil, nil
	}
	args := []any{nameWeight, descriptionWeight, bodyWeight, ftsEscape(query)}
	args = append(args, stringArgs(roots)...)
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+nativeColumns+`,
		       bm25(native_docs_fts, ?, ?, ?) AS score,
		       snippet(native_docs_fts, 2, '', '', '…', 24) AS snip
		  FROM native_docs_fts
		  JOIN native_docs d ON d.rowid = native_docs_fts.rowid
		 WHERE native_docs_fts MATCH ?
		   AND d.root IN (`+placeholders(len(roots))+`)
		 ORDER BY score LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("native search: %w", err)
	}
	defer rows.Close()

	var out []native.Hit
	for rows.Next() {
		var h native.Hit
		dest := append(scanNativeDoc(&h.Doc), &h.Score, &h.Snippet)
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("scan native hit: %w", err)
		}
		h.Score = -h.Score // bm25() is negative, lower is better
		out = append(out, h)
	}
	return out, rows.Err()
}

func (s *nativeStore) Get(ctx context.Context, id string, roots []string) (*native.Doc, error) {
	if len(roots) == 0 {
		return nil, native.ErrNotFound
	}
	args := append([]any{id}, stringArgs(roots)...)
	var doc native.Doc
	err := s.db.QueryRowContext(ctx, `
		SELECT `+nativeColumns+` FROM native_docs d
		 WHERE d.id = ? AND d.root IN (`+placeholders(len(roots))+`)`, args...).
		Scan(scanNativeDoc(&doc)...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, native.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("native get: %w", err)
	}
	return &doc, nil
}

func (s *nativeStore) Counts(ctx context.Context, roots []string) (notes, repos int, err error) {
	if len(roots) == 0 {
		return 0, 0, nil
	}
	err = s.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COUNT(DISTINCT project) FROM native_docs
		 WHERE root IN (`+placeholders(len(roots))+`)`, stringArgs(roots)...).Scan(&notes, &repos)
	if err != nil {
		return 0, 0, fmt.Errorf("native counts: %w", err)
	}
	return notes, repos, nil
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func stringArgs(values []string) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}
