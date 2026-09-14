package platform

import (
	"context"
	"net/http"

	"github.com/anthony-hopkins/tomb/internal/auth"
)

// Context keys are unexported struct types so no other package can collide
// with or forge them.
type (
	sessionKey struct{}
	profileKey struct{}
	csrfKey    struct{}
)

// withSession attaches the resolved session to the request context.
func withSession(r *http.Request, sess auth.Session) *http.Request {
	return r.WithContext(ContextWithSession(r.Context(), sess))
}

// ContextWithSession attaches a session to a context.
//
// The core calls this from its own middleware. It is exported so tests outside
// this package can drive gated routes without standing up a database, and so
// any future middleware can establish a session the same way the core does.
func ContextWithSession(ctx context.Context, sess auth.Session) context.Context {
	return context.WithValue(ctx, sessionKey{}, sess)
}

// SessionFrom returns the session for the request, if the viewer is signed in.
func SessionFrom(ctx context.Context) (auth.Session, bool) {
	sess, ok := ctx.Value(sessionKey{}).(auth.Session)
	return sess, ok
}

// withProfile attaches this request's live character fetch.
func withProfile(r *http.Request, p Profile) *http.Request {
	return r.WithContext(ContextWithProfile(r.Context(), p))
}

// ContextWithProfile attaches a profile to a context. Exported for the same
// reason ContextWithSession is: an app's tests need to stand a viewer up --
// member, officer -- without a database or a Blizzard client behind them.
func ContextWithProfile(ctx context.Context, p Profile) context.Context {
	return context.WithValue(ctx, profileKey{}, p)
}

// ProfileFrom returns the characters fetched for this request.
//
// Apps read the profile from here rather than calling Blizzard themselves, so
// a view costs exactly one 1+N fetch no matter how many components need the
// data (FR-016).
func ProfileFrom(ctx context.Context) (Profile, bool) {
	p, ok := ctx.Value(profileKey{}).(Profile)
	return p, ok
}

// withCSRFToken attaches the token so templates can embed it.
func withCSRFToken(r *http.Request, token string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), csrfKey{}, token))
}

// CSRFTokenFrom returns the CSRF token for this request, or "".
func CSRFTokenFrom(ctx context.Context) string {
	token, _ := ctx.Value(csrfKey{}).(string)
	return token
}
