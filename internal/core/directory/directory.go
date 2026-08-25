// Package directory implements spaces, channels, members, and roles.
// Capabilities are named strings on a role, never permission integers — see
// docs/architecture.md section 2. This first implementation is in-memory; a
// Postgres-backed store lands separately (internal/store/postgres).
package directory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"

	v1 "github.com/seishinapp/seishin/api/proto/seishin/v1"
)

var ErrSpaceNotFound = errors.New("directory: space not found")

// Service implements DirectoryService.
type Service struct {
	mu       sync.RWMutex
	spaces   map[string]*v1.Space
	channels map[string][]*v1.Channel // by space_id
	members  map[string][]*v1.Member  // by space_id
	roles    map[string][]*v1.Role    // by space_id
}

// NewSeededService returns a Service pre-populated with one default space,
// so a freshly started server has something to browse. Real space/channel
// creation (beyond this seed) is exercised through CreateChannel.
func NewSeededService() *Service {
	s := &Service{
		spaces:   make(map[string]*v1.Space),
		channels: make(map[string][]*v1.Channel),
		members:  make(map[string][]*v1.Member),
		roles:    make(map[string][]*v1.Role),
	}

	spaceID := "spc_" + randomID()
	s.spaces[spaceID] = &v1.Space{SpaceId: spaceID, Name: "Home"}
	s.channels[spaceID] = []*v1.Channel{
		{
			ChannelId: "chn_" + randomID(),
			SpaceId:   spaceID,
			Name:      "general",
			Kind:      v1.ChannelKind_CHANNEL_KIND_TEXT,
		},
		{
			ChannelId:     "chn_" + randomID(),
			SpaceId:       spaceID,
			Name:          "Lounge",
			Kind:          v1.ChannelKind_CHANNEL_KIND_VOICE,
			SpatialPolicy: v1.SpatialPolicy_SPATIAL_POLICY_FLAT,
		},
	}
	s.roles[spaceID] = []*v1.Role{
		{RoleId: "rol_" + randomID(), Name: "member", Capabilities: []string{"channel.read", "message.write"}},
	}

	return s
}

func (s *Service) ListSpaces(ctx context.Context, req *v1.ListSpacesRequest) (*v1.ListSpacesResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	spaces := make([]*v1.Space, 0, len(s.spaces))
	for _, sp := range s.spaces {
		spaces = append(spaces, sp)
	}
	return &v1.ListSpacesResponse{Spaces: spaces}, nil
}

func (s *Service) GetSpace(ctx context.Context, req *v1.GetSpaceRequest) (*v1.GetSpaceResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sp, ok := s.spaces[req.SpaceId]
	if !ok {
		return nil, ErrSpaceNotFound
	}
	return &v1.GetSpaceResponse{Space: sp}, nil
}

func (s *Service) ListChannels(ctx context.Context, req *v1.ListChannelsRequest) (*v1.ListChannelsResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if _, ok := s.spaces[req.SpaceId]; !ok {
		return nil, ErrSpaceNotFound
	}
	return &v1.ListChannelsResponse{Channels: s.channels[req.SpaceId]}, nil
}

// CreateChannel does not yet check the caller's capabilities — that
// requires a capability-aware caller identity in the request context, which
// the gateway does not populate yet, and the Policy engine (bot-api
// capability, not yet built). Every call currently succeeds.
func (s *Service) CreateChannel(ctx context.Context, req *v1.CreateChannelRequest) (*v1.CreateChannelResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.spaces[req.SpaceId]; !ok {
		return nil, ErrSpaceNotFound
	}

	ch := &v1.Channel{
		ChannelId:     "chn_" + randomID(),
		SpaceId:       req.SpaceId,
		Name:          req.Name,
		Kind:          req.Kind,
		SpatialPolicy: req.SpatialPolicy,
	}
	s.channels[req.SpaceId] = append(s.channels[req.SpaceId], ch)

	return &v1.CreateChannelResponse{Channel: ch}, nil
}

func (s *Service) ListMembers(ctx context.Context, req *v1.ListMembersRequest) (*v1.ListMembersResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if _, ok := s.spaces[req.SpaceId]; !ok {
		return nil, ErrSpaceNotFound
	}
	return &v1.ListMembersResponse{Members: s.members[req.SpaceId]}, nil
}

func (s *Service) ListRoles(ctx context.Context, req *v1.ListRolesRequest) (*v1.ListRolesResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if _, ok := s.spaces[req.SpaceId]; !ok {
		return nil, ErrSpaceNotFound
	}
	return &v1.ListRolesResponse{Roles: s.roles[req.SpaceId]}, nil
}

func randomID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failing is unrecoverable
	}
	return hex.EncodeToString(b)
}
