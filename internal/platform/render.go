package platform

import (
	"bytes"
	"html/template"
	"net/http"
)

// PageData is what every shared template receives.
type PageData struct {
	Title     string
	Nav       []NavItem
	SignedIn  bool
	BattleTag string
	CSRFToken string

	// Message carries an explanatory or error string where the page needs one.
	Message string

	// Content is an app's rendered body, injected into the shared shell.
	Content template.HTML
}

// NavItem is one navigation entry, built from AppMeta (contracts/app-registration.md
// guarantee 4).
type NavItem struct {
	Label string
	Href  string
}

// renderPage writes a shared template through the layout.
func (c *Core) renderPage(w http.ResponseWriter, r *http.Request, status int, name string, data PageData) {
	c.decorate(r, &data)

	var buf bytes.Buffer
	if err := c.Templates.ExecutePage(&buf, name, data); err != nil {
		c.Deps.Logger.Error("render template", "template", name, "error", err)
		http.Error(w, "Something went wrong rendering this page.", http.StatusInternalServerError)
		return
	}
	writeHTML(w, status, &buf)
}

// RenderInLayout wraps an app's pre-rendered body in the shared shell. It is
// handed to apps through Deps so they never own the site chrome
// (contracts/app-registration.md).
func (c *Core) RenderInLayout(w http.ResponseWriter, r *http.Request, status int, title string, content template.HTML) {
	data := PageData{Title: title, Content: content}
	c.decorate(r, &data)

	var buf bytes.Buffer
	if err := c.Templates.ExecuteLayout(&buf, data); err != nil {
		c.Deps.Logger.Error("render layout", "error", err)
		http.Error(w, "Something went wrong rendering this page.", http.StatusInternalServerError)
		return
	}
	writeHTML(w, status, &buf)
}

// decorate fills in what every page shares: the navigation, the CSRF token for
// the logout form, and who is signed in.
func (c *Core) decorate(r *http.Request, data *PageData) {
	data.Nav = c.navFor(r)
	data.CSRFToken = CSRFTokenFrom(r.Context())
	if sess, ok := SessionFrom(r.Context()); ok {
		data.SignedIn = true
		data.BattleTag = sess.User.BattleTag
	}
}

// writeHTML sends a fully rendered page.
//
// Rendering to a buffer first and only then writing is what keeps a template
// error from emitting half a page on top of an already-sent 200. Once the
// buffer is complete the write can fail only for reasons on the client's side
// -- a closed connection -- and there is nobody left to report that to.
func writeHTML(w http.ResponseWriter, status int, buf *bytes.Buffer) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

// RenderLoginFailed satisfies auth.Renderer: login did not complete (FR-009).
func (c *Core) RenderLoginFailed(w http.ResponseWriter, r *http.Request, status int, reason string) {
	c.renderPage(w, r, status, "login-failed.html", PageData{
		Title:   "Sign-in not completed",
		Message: reason,
	})
}

// RenderError satisfies auth.Renderer: the generic retry-able error (FR-010).
func (c *Core) RenderError(w http.ResponseWriter, r *http.Request, status int, reason string) {
	c.renderPage(w, r, status, "error.html", PageData{
		Title:   "Something went wrong",
		Message: reason,
	})
}

// RenderOfficersOnly is the FR-021 refusal: a member, but not an officer, on
// a page for officers. 403, because that is what it is; the page shell and
// navigation still render, because they are still a member everywhere else.
func (c *Core) RenderOfficersOnly(w http.ResponseWriter, r *http.Request) {
	c.renderPage(w, r, http.StatusForbidden, "error.html", PageData{
		Title: "Officers only",
		Message: "This page is for the guild master and officers. Everything else on " +
			"the site is still yours.",
	})
}

// RenderNonMember is the FR-013a denial page: no app is reachable, and the only
// action offered is logging out (scenario 7).
func (c *Core) RenderNonMember(w http.ResponseWriter, r *http.Request, status int) {
	c.renderPage(w, r, status, "non-member.html", PageData{
		Title: "TOMB members only",
		Message: "This site is for members of the TOMB guild. Your Battle.net " +
			"account does not have a character in TOMB, so there is nothing here for you yet.",
	})
}
