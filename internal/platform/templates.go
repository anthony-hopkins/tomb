package platform

import (
	"embed"
	"fmt"
	"html/template"
	"io"
	"net/http"
)

//go:embed templates
var coreTemplateFS embed.FS

//go:embed static
var staticFS embed.FS

// StaticHandler serves the embedded stylesheet. Embedding keeps the binary
// self-contained, so it does not depend on its working directory at runtime.
func StaticHandler() http.Handler {
	return http.FileServer(http.FS(staticFS))
}

// corePages are the shared pages the platform itself owns. Each is parsed into
// its own template set alongside the layout, because every page file defines a
// template named "content" — one combined set would let them collide.
var corePages = []string{
	"landing.html",
	"non-member.html",
	"login-failed.html",
	"error.html",
}

// PageSet holds one parsed template set per core page, plus a layout-only set
// used to wrap a pre-rendered app body.
type PageSet struct {
	pages      map[string]*template.Template
	layoutOnly *template.Template
}

// LoadTemplates parses the embedded core templates.
func LoadTemplates() (*PageSet, error) {
	ps := &PageSet{pages: make(map[string]*template.Template, len(corePages))}

	for _, page := range corePages {
		t, err := template.New("layout").ParseFS(coreTemplateFS,
			"templates/layout.html", "templates/"+page)
		if err != nil {
			return nil, fmt.Errorf("parse template %s: %w", page, err)
		}
		ps.pages[page] = t
	}

	// An app renders its own body and hands it over as PageData.Content, so the
	// layout is executed on its own. The layout's "content" block supplies an
	// empty default, which keeps this parse valid.
	layoutOnly, err := template.New("layout").ParseFS(coreTemplateFS, "templates/layout.html")
	if err != nil {
		return nil, fmt.Errorf("parse layout: %w", err)
	}
	ps.layoutOnly = layoutOnly

	return ps, nil
}

// ExecutePage renders one core page through the layout.
func (ps *PageSet) ExecutePage(w io.Writer, name string, data PageData) error {
	t, ok := ps.pages[name]
	if !ok {
		return fmt.Errorf("unknown page template %q", name)
	}
	return t.ExecuteTemplate(w, "layout", data)
}

// ExecuteLayout renders a pre-rendered app body inside the shared shell.
func (ps *PageSet) ExecuteLayout(w io.Writer, data PageData) error {
	return ps.layoutOnly.ExecuteTemplate(w, "layout", data)
}
