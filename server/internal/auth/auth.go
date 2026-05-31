// Package auth provides user management (bcrypt-hashed passwords), JWT issuance
// and verification, role-based access control (admin/editor), and HTTP
// middleware. It replaces the legacy "user:secret" pseudo-token scheme.
package auth

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"moesekai/server/internal/db"
)

// Roles.
const (
	RoleAdmin  = "admin"
	RoleEditor = "editor"
)

func ValidRole(r string) bool { return r == RoleAdmin || r == RoleEditor }

// User is a console account.
type User struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	Role      string `json:"role"`
	CreatedAt int64  `json:"createdAt"`
}

var (
	ErrUserExists   = errors.New("user already exists")
	ErrUserNotFound = errors.New("user not found")
	ErrInvalidCreds = errors.New("invalid credentials")
	ErrLastAdmin    = errors.New("cannot remove the last admin")
)

// Auth manages users and tokens.
type Auth struct {
	db        *db.DB
	jwtSecret []byte
	tokenTTL  time.Duration
}

func New(database *db.DB, jwtSecret string, ttl time.Duration) *Auth {
	if ttl <= 0 {
		ttl = 7 * 24 * time.Hour
	}
	return &Auth{db: database, jwtSecret: []byte(jwtSecret), tokenTTL: ttl}
}

// ---- User CRUD ----

// CreateUser adds a user with a bcrypt-hashed password.
func (a *Auth) CreateUser(username, password, role string) (*User, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return nil, errors.New("username and password required")
	}
	if !ValidRole(role) {
		role = RoleEditor
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	res, err := a.db.Exec(
		`INSERT INTO users (username, password_hash, role, created_at) VALUES (?, ?, ?, ?)`,
		username, string(hash), role, now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, ErrUserExists
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &User{ID: id, Username: username, Role: role, CreatedAt: now}, nil
}

// ListUsers returns all users ordered by id (no password hashes).
func (a *Auth) ListUsers() ([]User, error) {
	rows, err := a.db.Query(`SELECT id, username, role, created_at FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.Role, &u.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// SetPassword updates a user's password.
func (a *Auth) SetPassword(username, password string) error {
	if password == "" {
		return errors.New("password required")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	res, err := a.db.Exec(`UPDATE users SET password_hash = ? WHERE username = ?`, string(hash), username)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrUserNotFound
	}
	return nil
}

// SetRole updates a user's role, refusing to demote the last admin.
func (a *Auth) SetRole(username, role string) error {
	if !ValidRole(role) {
		return fmt.Errorf("invalid role: %s", role)
	}
	if role != RoleAdmin {
		if err := a.guardLastAdmin(username); err != nil {
			return err
		}
	}
	res, err := a.db.Exec(`UPDATE users SET role = ? WHERE username = ?`, role, username)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrUserNotFound
	}
	return nil
}

// DeleteUser removes a user, refusing to remove the last admin.
func (a *Auth) DeleteUser(username string) error {
	if err := a.guardLastAdmin(username); err != nil {
		return err
	}
	res, err := a.db.Exec(`DELETE FROM users WHERE username = ?`, username)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrUserNotFound
	}
	return nil
}

// guardLastAdmin returns ErrLastAdmin if username is currently the only admin.
func (a *Auth) guardLastAdmin(username string) error {
	var role string
	err := a.db.QueryRow(`SELECT role FROM users WHERE username = ?`, username).Scan(&role)
	if err == sql.ErrNoRows {
		return nil // not a user; nothing to guard
	}
	if err != nil {
		return err
	}
	if role != RoleAdmin {
		return nil
	}
	var adminCount int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM users WHERE role = ?`, RoleAdmin).Scan(&adminCount); err != nil {
		return err
	}
	if adminCount <= 1 {
		return ErrLastAdmin
	}
	return nil
}

// CountUsers returns the total number of users (used for first-run seeding).
func (a *Auth) CountUsers() (int, error) {
	var n int
	err := a.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// ---- Authentication ----

// Authenticate verifies credentials and returns the user.
func (a *Auth) Authenticate(username, password string) (*User, error) {
	var u User
	var hash string
	err := a.db.QueryRow(
		`SELECT id, username, password_hash, role, created_at FROM users WHERE username = ?`,
		strings.TrimSpace(username)).Scan(&u.ID, &u.Username, &hash, &u.Role, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrInvalidCreds
	}
	if err != nil {
		return nil, err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return nil, ErrInvalidCreds
	}
	return &u, nil
}

// GetUser returns a user by username (no password hash).
func (a *Auth) GetUser(username string) (*User, error) {
	var u User
	err := a.db.QueryRow(
		`SELECT id, username, role, created_at FROM users WHERE username = ?`,
		username).Scan(&u.ID, &u.Username, &u.Role, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// ---- JWT ----

type Claims struct {
	Username string `json:"username"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

// IssueToken creates a signed JWT for the user.
func (a *Auth) IssueToken(u *User) (string, time.Time, error) {
	expiresAt := time.Now().Add(a.tokenTTL)
	claims := Claims{
		Username: u.Username,
		Role:     u.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   u.Username,
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(a.jwtSecret)
	return signed, expiresAt, err
}

// VerifyToken parses and validates a JWT, returning its claims.
func (a *Auth) VerifyToken(tokenStr string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return a.jwtSecret, nil
	})
	if err != nil || !token.Valid {
		return nil, ErrInvalidCreds
	}
	return claims, nil
}
