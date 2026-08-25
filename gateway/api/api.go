// Package api is the Public API Gateway from docs/architecture.md's
// hourglass diagram: it speaks Connect (HTTP+JSON, gRPC, gRPC-Web) on one
// side and calls core services with plain Canonical Platform API messages
// on the other. It carries no logic of its own beyond that translation —
// seishin-web talks to nothing else, and has no access a third-party bot
// with the same capabilities wouldn't have.
package api

import (
	"context"
	"errors"
	"net/http"

	"connectrpc.com/connect"

	v1 "github.com/seishinapp/seishin/api/proto/seishin/v1"
	"github.com/seishinapp/seishin/api/proto/seishin/v1/seishinv1connect"
	"github.com/seishinapp/seishin/internal/core/directory"
	"github.com/seishinapp/seishin/internal/core/session"
)

// NewHandler builds the HTTP handler serving SessionService and
// DirectoryService over Connect.
func NewHandler(sessionSvc *session.Service, directorySvc *directory.Service) http.Handler {
	mux := http.NewServeMux()

	sessionPath, sessionHandler := seishinv1connect.NewSessionServiceHandler(&sessionAdapter{sessionSvc})
	mux.Handle(sessionPath, withCORS(sessionHandler))

	directoryPath, directoryHandler := seishinv1connect.NewDirectoryServiceHandler(&directoryAdapter{directorySvc})
	mux.Handle(directoryPath, withCORS(directoryHandler))

	return mux
}

// withCORS allows a web client served from any origin to reach this API
// directly, matching restriction #5: nothing seishin-web can do is
// unreachable by a third-party client.
func withCORS(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Connect-Protocol-Version")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}

type sessionAdapter struct {
	svc *session.Service
}

func (a *sessionAdapter) Hello(ctx context.Context, req *connect.Request[v1.HelloRequest]) (*connect.Response[v1.HelloResponse], error) {
	resp, err := a.svc.Hello(ctx, req.Msg)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(resp), nil
}

func (a *sessionAdapter) Authenticate(ctx context.Context, req *connect.Request[v1.AuthenticateRequest]) (*connect.Response[v1.AuthenticateResponse], error) {
	resp, err := a.svc.Authenticate(ctx, req.Msg)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	return connect.NewResponse(resp), nil
}

func (a *sessionAdapter) Resume(ctx context.Context, req *connect.Request[v1.ResumeRequest]) (*connect.Response[v1.ResumeResponse], error) {
	resp, err := a.svc.Resume(ctx, req.Msg)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	return connect.NewResponse(resp), nil
}

type directoryAdapter struct {
	svc *directory.Service
}

func (a *directoryAdapter) ListSpaces(ctx context.Context, req *connect.Request[v1.ListSpacesRequest]) (*connect.Response[v1.ListSpacesResponse], error) {
	resp, err := a.svc.ListSpaces(ctx, req.Msg)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(resp), nil
}

func (a *directoryAdapter) GetSpace(ctx context.Context, req *connect.Request[v1.GetSpaceRequest]) (*connect.Response[v1.GetSpaceResponse], error) {
	resp, err := a.svc.GetSpace(ctx, req.Msg)
	if err != nil {
		return nil, mapDirectoryError(err)
	}
	return connect.NewResponse(resp), nil
}

func (a *directoryAdapter) ListChannels(ctx context.Context, req *connect.Request[v1.ListChannelsRequest]) (*connect.Response[v1.ListChannelsResponse], error) {
	resp, err := a.svc.ListChannels(ctx, req.Msg)
	if err != nil {
		return nil, mapDirectoryError(err)
	}
	return connect.NewResponse(resp), nil
}

func (a *directoryAdapter) CreateChannel(ctx context.Context, req *connect.Request[v1.CreateChannelRequest]) (*connect.Response[v1.CreateChannelResponse], error) {
	resp, err := a.svc.CreateChannel(ctx, req.Msg)
	if err != nil {
		return nil, mapDirectoryError(err)
	}
	return connect.NewResponse(resp), nil
}

func (a *directoryAdapter) ListMembers(ctx context.Context, req *connect.Request[v1.ListMembersRequest]) (*connect.Response[v1.ListMembersResponse], error) {
	resp, err := a.svc.ListMembers(ctx, req.Msg)
	if err != nil {
		return nil, mapDirectoryError(err)
	}
	return connect.NewResponse(resp), nil
}

func (a *directoryAdapter) ListRoles(ctx context.Context, req *connect.Request[v1.ListRolesRequest]) (*connect.Response[v1.ListRolesResponse], error) {
	resp, err := a.svc.ListRoles(ctx, req.Msg)
	if err != nil {
		return nil, mapDirectoryError(err)
	}
	return connect.NewResponse(resp), nil
}

// mapDirectoryError translates a directory package error into the Connect
// error code an HTTP client can act on.
func mapDirectoryError(err error) error {
	if errors.Is(err, directory.ErrSpaceNotFound) {
		return connect.NewError(connect.CodeNotFound, err)
	}
	return connect.NewError(connect.CodeInternal, err)
}
