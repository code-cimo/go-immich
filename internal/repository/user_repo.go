// Package repository is the only layer that speaks SQL. Handlers and services
// never build queries themselves; every method takes a context.Context so
// request cancellation propagates down to SQLite driver reads.
package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"go-immich/internal/models"
)

// UserRepo provides typed access to the users table.
type UserRepo struct{ db *sql.DB }

// NewUserRepo builds the repository around an opened pool.
func NewUserRepo(db *sql.DB) *UserRepo { return &UserRepo{db: db} }

const userColumns = `id, email, password_hash, name, avatar_path, is_admin,
	is_active, quota_bytes, created_at, updated_at`

func scanUser(row interface{ Scan(...any) error }) (*models.User, error) {
	var (
		u        models.User
		isAdmin  int64
		isActive int64
		quota    int64
		created  string
		updated  string
	)
	err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &u.AvatarPath,
		&isAdmin, &isActive, &quota, &created, &updated)
	if err != nil {
		return nil, err
	}
	u.IsAdmin = isAdmin != 0
	u.IsActive = isActive != 0
	u.QuotaSizeInBytes = uint64(quota)
	u.CreatedAt = parseTS(created)
	u.UpdatedAt = parseTS(updated)
	return &u, nil
}

// ByID returns nil (not an error) when the user does not exist - keeps the
// auth middleware free of sql.ErrNoRows plumbing.
func (r *UserRepo) ByID(ctx context.Context, id string) (*models.User, error) {
	row := r.db.QueryRowContext(ctx,
		"SELECT "+userColumns+" FROM users WHERE id = ?", id)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return u, err
}

// ByEmail looks up the login principal (case-insensitive e-mail, matching
// Postgres citext behaviour of the original Immich schema).
func (r *UserRepo) ByEmail(ctx context.Context, email string) (*models.User, error) {
	row, err := r.db.QueryContext(ctx,
		"SELECT "+userColumns+" FROM users WHERE email = ? COLLATE NOCASE LIMIT 1", email)
	if err != nil {
		return nil, fmt.Errorf("repository: user by email: %w", err)
	}
	defer func() { _ = row.Close() }()
	if !row.Next() {
		return nil, nil
	}
	u, err := scanUser(row)
	if err != nil {
		return nil, fmt.Errorf("repository: user by email: %w", err)
	}
	return u, row.Err()
}

// Create inserts a new user. Callers hash the password beforehand.
func (r *UserRepo) Create(ctx context.Context, u *models.User) error {
	now := time.Now().UTC()
	if u.CreatedAt.IsZero() {
		u.CreatedAt = now
	}
	u.UpdatedAt = now
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO users (id, email, password_hash, name, avatar_path, is_admin,
			is_active, quota_bytes, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ID, u.Email, u.PasswordHash, u.Name, u.AvatarPath, b2i(u.IsAdmin),
		b2i(u.IsActive), int64(u.QuotaSizeInBytes), formatTS(u.CreatedAt), formatTS(u.UpdatedAt))
	if err != nil {
		return fmt.Errorf("repository: create user: %w", err)
	}
	return nil
}

// UpdatePassword swaps the bcrypt hash for one user.
func (r *UserRepo) UpdatePassword(ctx context.Context, userID, hash string) error {
	res, err := r.db.ExecContext(ctx,
		"UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?",
		hash, formatTS(time.Now().UTC()), userID)
	if err != nil {
		return fmt.Errorf("repository: update password: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// UpdateName changes the display name.
func (r *UserRepo) UpdateName(ctx context.Context, userID, name string) error {
	_, err := r.db.ExecContext(ctx,
		"UPDATE users SET name = ?, updated_at = ? WHERE id = ?",
		name, formatTS(time.Now().UTC()), userID)
	return err
}

// Count total users (used by onboarding check + stats).
func (r *UserRepo) Count(ctx context.Context) (int64, error) {
	var n int64
	err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&n)
	return n, err
}

// ---------- shared helpers ----------

// formatTS renders times the way the official API stores them in Postgres but
// normalised to UTC without timezone suffix noise; clients accept RFC3339Nano.
func formatTS(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02 15:04:05.000000")
}

// parseTS accepts both our storage format and ISO variants written by the
// legacy importer.
func parseTS(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999",
		time.RFC3339Nano,
		"2006-01-02T15:04:05.999999999",
		"2006-01-02",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
