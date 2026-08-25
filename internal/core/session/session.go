// Package session implements session establishment and resume: the
// Hello/Authenticate/Resume exchange described in docs/protocol.md section
// 3, served here over this repository's interim HTTP surface rather than
// the native CXP/1 transport. Presence (also part of this capability) has
// no implementation yet — it depends on NATS JetStream KV, not built.
package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	v1 "github.com/seishinapp/seishin/api/proto/seishin/v1"
	"github.com/seishinapp/seishin/internal/core/identity"
)

const (
	nonceTTL       = 2 * time.Minute
	resumeTokenTTL = 60 * time.Second
)

var (
	ErrUnknownNonce     = errors.New("session: nonce not issued, already used, or expired")
	ErrInvalidSignature = errors.New("session: signature verification failed")
	ErrResumeExpired    = errors.New("session: resume token unknown, expired, or mismatched")
)

// defaultCapabilities is a placeholder capability set granted to every
// authenticated session, until capabilities are resolved from a member's
// Directory roles (that resolution needs Directory membership to exist
// first).
var defaultCapabilities = []string{"directory.read", "presence.read", "presence.write"}

type nonceEntry struct {
	issuedAt time.Time
}

type sessionEntry struct {
	session      *v1.Session
	resumeExpiry time.Time
}

// Service implements SessionService.
type Service struct {
	mu       sync.Mutex
	nonces   map[string]nonceEntry
	sessions map[string]*sessionEntry // keyed by session_id
}

func NewService() *Service {
	return &Service{
		nonces:   make(map[string]nonceEntry),
		sessions: make(map[string]*sessionEntry),
	}
}

func (s *Service) Hello(ctx context.Context, req *v1.HelloRequest) (*v1.HelloResponse, error) {
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.nonces[string(nonce)] = nonceEntry{issuedAt: time.Now()}
	s.mu.Unlock()

	return &v1.HelloResponse{
		ServerVersion: "0.1.0",
		ServerInfo:    &v1.ServerInfo{Name: "seishin"},
		Nonce:         nonce,
		AuthMethods:   []string{"ed25519"},
	}, nil
}

func (s *Service) Authenticate(ctx context.Context, req *v1.AuthenticateRequest) (*v1.AuthenticateResponse, error) {
	s.mu.Lock()
	entry, ok := s.nonces[string(req.Nonce)]
	if ok {
		delete(s.nonces, string(req.Nonce)) // single use
	}
	s.mu.Unlock()

	if !ok || time.Since(entry.issuedAt) > nonceTTL {
		return nil, ErrUnknownNonce
	}

	if !identity.VerifySignature(req.IdentityPublicKey, req.Nonce, req.Signature) {
		return nil, ErrInvalidSignature
	}

	userID, err := identity.UserID(req.IdentityPublicKey)
	if err != nil {
		return nil, err
	}

	// TODO(home-provider): validate req.HomeProviderToken and populate
	// Identity.Handle once a home provider integration exists. Guests and
	// keys with no token get no handle, per docs/architecture.md section 6.

	sess := &v1.Session{
		SessionId: randomToken(),
		Identity: &v1.Identity{
			PublicKey: req.IdentityPublicKey,
			UserId:    userID,
		},
		Capabilities: defaultCapabilities,
		ResumeToken:  randomToken(),
	}

	s.mu.Lock()
	s.sessions[sess.SessionId] = &sessionEntry{
		session:      sess,
		resumeExpiry: time.Now().Add(resumeTokenTTL),
	}
	s.mu.Unlock()

	return &v1.AuthenticateResponse{Session: sess}, nil
}

func (s *Service) Resume(ctx context.Context, req *v1.ResumeRequest) (*v1.ResumeResponse, error) {
	s.mu.Lock()
	entry, ok := s.sessions[req.SessionId]
	s.mu.Unlock()

	if !ok || entry.session.ResumeToken != req.ResumeToken || time.Now().After(entry.resumeExpiry) {
		return nil, ErrResumeExpired
	}

	return &v1.ResumeResponse{Session: entry.session}, nil
}

func randomToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failing is unrecoverable
	}
	return hex.EncodeToString(b)
}
