package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/argon2"
)

//go:embed schema.sql
var schema string

var (
	ErrLogin     = errors.New("invalid credentials or inactive account")
	ErrLastAdmin = errors.New("cannot disable or demote the last active administrator")
	ErrInput     = errors.New("invalid account fields")
	userPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{2,63}$`)
)

type Store struct {
	Pool   *pgxpool.Pool
	hashes chan struct{}
}

func NewStore(pool *pgxpool.Pool) *Store           { return &Store{pool, make(chan struct{}, 4)} }
func (s *Store) Migrate(ctx context.Context) error { _, err := s.Pool.Exec(ctx, schema); return err }

func (s *Store) password(ctx context.Context, plain string, salt []byte) ([]byte, error) {
	select {
	case s.hashes <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-s.hashes }()
	return argon2.IDKey([]byte(plain), salt, 2, 32*1024, 1, 32), nil
}

func (s *Store) CreateAccount(ctx context.Context, actor, username, name, role, pw string) (Principal, error) {
	u := Principal{Username: strings.ToLower(strings.TrimSpace(username)), Name: strings.TrimSpace(name), Role: role}
	if !userPattern.MatchString(u.Username) || u.Name == "" || len(u.Name) > 240 || (role != "admin" && role != "member") || len(pw) < 12 || len(pw) > 256 {
		return u, ErrInput
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return u, err
	}
	hash, err := s.password(ctx, pw, salt)
	if err != nil {
		return u, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return u, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(1847763301)`); err != nil {
		return u, err
	}
	if actor == "" {
		var any bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wb.account)`).Scan(&any); err != nil {
			return u, err
		}
		if any || role != "admin" {
			return u, ErrInput
		}
	}
	err = tx.QueryRow(ctx, `INSERT INTO wb.account(username,name,role,password_hash,salt) VALUES ($1,$2,$3,$4,$5) RETURNING id::text`, u.Username, u.Name, u.Role, hash, salt).Scan(&u.ID)
	if err != nil {
		return u, err
	}
	if err = audit(ctx, tx, actor, "account.create", u.ID); err != nil {
		return u, err
	}
	return u, tx.Commit(ctx)
}

func audit(ctx context.Context, tx pgx.Tx, actor, action, target string) error {
	_, err := tx.Exec(ctx, `INSERT INTO wb.audit(actor,action,target) VALUES($1,$2,$3)`, actor, action, target)
	return err
}

func (s *Store) Login(ctx context.Context, user, pw, _ string) (Principal, error) {
	var id string
	var salt, hash []byte
	err := s.Pool.QueryRow(ctx, `SELECT id::text,salt,password_hash FROM wb.account WHERE username=$1 AND active`, strings.ToLower(strings.TrimSpace(user))).Scan(&id, &salt, &hash)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, err
	}
	if len(salt) == 0 {
		salt = make([]byte, 16)
		hash = make([]byte, 32)
	}
	got, err := s.password(ctx, pw, salt)
	if err != nil {
		return Principal{}, err
	}
	if subtle.ConstantTimeCompare(got, hash) != 1 || id == "" {
		return Principal{}, ErrLogin
	}
	return s.Principal(ctx, id)
}

func (s *Store) Principal(ctx context.Context, id string) (Principal, error) {
	u := Principal{Projects: map[string]string{}}
	err := s.Pool.QueryRow(ctx, `SELECT id::text,username,name,role FROM wb.account WHERE id=$1 AND active`, id).Scan(&u.ID, &u.Username, &u.Name, &u.Role)
	if err != nil {
		return u, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT project_id::text,role FROM wb.membership WHERE account_id=$1`, id)
	if err != nil {
		return u, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, role string
		if err := rows.Scan(&id, &role); err != nil {
			return u, err
		}
		u.Projects[id] = role
	}
	return u, rows.Err()
}

func (s *Store) StartSession(ctx context.Context, id string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	h := sha256.Sum256([]byte(token))
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `DELETE FROM wb.session WHERE account_id=$1 AND expires_at<=now()`, id); err != nil {
		return "", err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO wb.session(token_hash,account_id,expires_at) SELECT $1,id,$3 FROM wb.account WHERE id=$2 AND active`, h[:], id, time.Now().Add(12*time.Hour))
	if err != nil {
		return "", err
	}
	if tag.RowsAffected() != 1 {
		return "", ErrLogin
	}
	return token, tx.Commit(ctx)
}

func (s *Store) Session(ctx context.Context, token string) (Principal, error) {
	if len(token) != 43 {
		return Principal{}, ErrLogin
	}
	h := sha256.Sum256([]byte(token))
	var id string
	if err := s.Pool.QueryRow(ctx, `SELECT account_id::text FROM wb.session WHERE token_hash=$1 AND expires_at>now()`, h[:]).Scan(&id); err != nil {
		return Principal{}, err
	}
	return s.Principal(ctx, id)
}
func (s *Store) EndSession(ctx context.Context, token string) error {
	h := sha256.Sum256([]byte(token))
	_, err := s.Pool.Exec(ctx, `DELETE FROM wb.session WHERE token_hash=$1`, h[:])
	return err
}

func (s *Store) SetAccount(ctx context.Context, actor, id, role string, active bool) error {
	if role != "admin" && role != "member" {
		return ErrInput
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(1847763301)`); err != nil {
		return err
	}
	var prior string
	var on bool
	if err = tx.QueryRow(ctx, `SELECT role,active FROM wb.account WHERE id=$1 FOR UPDATE`, id).Scan(&prior, &on); err != nil {
		return err
	}
	if prior == "admin" && on && (role != "admin" || !active) {
		var n int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM wb.account WHERE active AND role='admin'`).Scan(&n); err != nil {
			return err
		}
		if n <= 1 {
			return ErrLastAdmin
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE wb.account SET role=$2,active=$3 WHERE id=$1`, id, role, active); err != nil {
		return err
	}
	if !active || prior != role {
		if _, err = tx.Exec(ctx, `DELETE FROM wb.session WHERE account_id=$1`, id); err != nil {
			return err
		}
	}
	if err = audit(ctx, tx, actor, "account.update", id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) SetMembership(ctx context.Context, actor, id, project, role string) error {
	if role != "member" && role != "manager" && role != "" {
		return ErrInput
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if role == "" {
		_, err = tx.Exec(ctx, `DELETE FROM wb.membership WHERE account_id=$1 AND project_id=$2`, id, project)
	} else {
		_, err = tx.Exec(ctx, `INSERT INTO wb.membership VALUES($1,$2,$3) ON CONFLICT(account_id,project_id) DO UPDATE SET role=excluded.role`, id, project, role)
	}
	if err != nil {
		return err
	}
	if err = audit(ctx, tx, actor, "membership.update", id+":"+project); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type Account struct {
	Principal
	Active bool `json:"active"`
}

func (s *Store) Accounts(ctx context.Context) ([]Account, error) {
	rows, err := s.Pool.Query(ctx, `SELECT a.id::text,a.username,a.name,a.role,a.active,coalesce((SELECT jsonb_object_agg(m.project_id::text,m.role) FROM wb.membership m WHERE m.account_id=a.id),'{}'::jsonb) FROM wb.account a ORDER BY username LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Account{}
	for rows.Next() {
		var a Account
		if err = rows.Scan(&a.ID, &a.Username, &a.Name, &a.Role, &a.Active, &a.Projects); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

type AuditEntry struct {
	Actor  string    `json:"actor"`
	Action string    `json:"action"`
	Target string    `json:"target"`
	At     time.Time `json:"at"`
}

func (s *Store) Audits(ctx context.Context) ([]AuditEntry, error) {
	rows, err := s.Pool.Query(ctx, `SELECT actor,action,target,happened_at FROM wb.audit ORDER BY id DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		if err = rows.Scan(&e.Actor, &e.Action, &e.Target, &e.At); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
