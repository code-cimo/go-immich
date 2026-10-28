// Package services holds business logic decoupled from HTTP: authentication
// (bcrypt + JWT issuance) and server-info payload assembly. Handlers stay thin.
package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"go-immich/config"
	"go-immich/internal/middleware"
	"go-immich/internal/models"
	"go-immich/internal/repository"
)

// AuthError wraps the three distinct login failure modes into one sentinel so
// handlers never leak which check failed (user enumeration protection).
var ErrInvalidCredentials = errors.New("auth: invalid credentials")

// AuthService verifies passwords and mints session tokens.
type AuthService struct {
	users  *repository.UserRepo
	cfg    *config.Config
}

// NewAuthService wires dependencies.
func NewAuthService(users *repository.UserRepo, cfg *config.Config) *AuthService {
	return &AuthService{users: users, cfg: cfg}
}

// HashPassword produces a bcrypt hash compatible with Immich's argon2->bcrypt
// fallback; legacy argon2 hashes are handled by the importer/reset flag.
func HashPassword(plain string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("services: hash password: %w", err)
	}
	return string(h), nil
}

// Login validates credentials and returns the user plus a signed JWT.
func (s *AuthService) Login(ctx context.Context, email, password string) (*models.User, string, error) {
	u, err := s.users.ByEmail(ctx, email)
	if err != nil {
		return nil, "", err
	}
	if u == nil {
		// Constant-ish work factor against timing enumeration: still run bcrypt.
		_ = bcrypt.CompareHashAndPassword([]byte("$2a$10$"+"x"+"x43invalidhashpaddingpaddingpaddingpaddingpad"), []byte(password))
		return nil, "", ErrInvalidCredentials
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		return nil, "", ErrInvalidCredentials
	}
	if !u.IsActive {
		return nil, "", ErrInvalidCredentials
	}

	token, err := s.SignToken(u)
	if err != nil {
		return nil, "", err
	}
	return u, token, nil
}

// SignToken creates the HS256 JWT stored as accessToken in the client. The
// payload carries user_id/email/name/isAdmin exactly as requested; Immich
// clients treat the token as opaque so extra claims are harmless.
func (s *AuthService) SignToken(u *models.User) (string, error) {
	now := time.Now().UTC()
	claims := middleware.Claims{
		Email:   u.Email,
		Name:    u.Name,
		IsAdmin: u.IsAdmin,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   u.ID,
			Issuer:    "immich-go",
			ID:        fmt.Sprintf("%d", now.UnixNano()),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now.Add(-time.Minute)), // tolerate clock skew
			ExpiresAt: jwt.NewNumericDate(now.Add(s.cfg.TokenValidity)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString([]byte(s.cfg.JWTSecret))
	if err != nil {
		return "", fmt.Errorf("services: sign token: %w", err)
	}
	return signed, nil
}

// ChangePassword re-hashes after verifying the current credential.
func (s *AuthService) ChangePassword(ctx context.Context, userID, oldPw, newPw string) error {
	u, err := s.users.ByID(ctx, userID)
	if err != nil || u == nil {
		return ErrInvalidCredentials
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(oldPw)); err != nil {
		return ErrInvalidCredentials
	}
	hash, err := HashPassword(newPw)
	if err != nil {
		return err
	}
	return s.users.UpdatePassword(ctx, userID, hash)
}

// EnsureAdmin seeds the bootstrap admin account on first start so mobile
// clients can immediately log in (Immich does this via onboarding wizard).
func (s *AuthService) EnsureAdmin(ctx context.Context) error {
	n, err := s.users.Count(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	hash, err := HashPassword(s.cfg.AdminPassword)
	if err != nil {
		return err
	}
	admin := &models.User{
		ID:           "admin-" + time.Now().UTC().Format("20060102"),
		Email:        s.cfg.AdminEmail,
		PasswordHash: hash,
		Name:         "Admin",
		IsAdmin:      true,
		IsActive:     true,
	}
	if err := s.users.Create(ctx, admin); err != nil {
		return fmt.Errorf("services: seed admin: %w", err)
	}
	return nil
}
