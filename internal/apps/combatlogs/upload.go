package combatlogs

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync"

	"github.com/anthony-hopkins/tomb/internal/fights"
	"github.com/anthony-hopkins/tomb/internal/platform"
)

// The upload protocol (contracts/http-routes.md, research D4): the browser
// cuts the log into pieces of fights.PieceBytes, compresses each on its own,
// and sends them in order. Every request is small, so nothing about the
// server's timeouts or the proxy's limits has to change for a file of any
// size; a dropped connection costs one piece; and the pieces, appended in
// order, are one valid gzip stream.

// rawLimit is the most a file may be before compression. Combat-log text
// compresses at least eight to one; twelve times the compressed limit is
// generous, and the compressed limit is enforced piece by piece regardless.
const rawLimit = 12 * platform.UploadLimitBytes

// gzipMagic opens every gzip member.
var gzipMagic = []byte{0x1f, 0x8b}

// locks serialises pieces of one upload: the browser sends them one at a
// time, but a retried request can overlap the original, and two appends to
// one file must not interleave.
var locks sync.Map

func lockFor(id int64) *sync.Mutex {
	m, _ := locks.LoadOrStore(id, &sync.Mutex{})
	return m.(*sync.Mutex)
}

type beginRequest struct {
	Filename    string `json:"filename"`
	Size        int64  `json:"size"`
	Fingerprint string `json:"fingerprint"`
}

type beginResponse struct {
	ID             int64  `json:"id"`
	State          string `json:"state"`
	PiecesTotal    int    `json:"pieces_total"`
	PiecesReceived int    `json:"pieces_received"`
	PieceBytes     int    `json:"piece_bytes"`
	URL            string `json:"url,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// guarded is the CSRF check every write needs, answering as JSON since the
// callers are the uploader script.
func (a *App) guarded(w http.ResponseWriter, r *http.Request) bool {
	if a.deps.CSRF == nil || !a.deps.CSRF.Verify(r) {
		a.deps.Logger.Warn("combat log write rejected: bad csrf token", "path", r.URL.Path)
		jsonError(w, http.StatusForbidden, "The request could not be verified. Reload the page and try again.")
		return false
	}
	return true
}

func uploadURL(id int64) string {
	return routePrefix + "/uploads/" + strconv.FormatInt(id, 10)
}

// begin answers a new upload id, or the upload this file already is.
func (a *App) begin(w http.ResponseWriter, r *http.Request) {
	if !a.guarded(w, r) {
		return
	}
	uid, tag := a.who(r)

	var req beginRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "The upload request could not be read.")
		return
	}
	fp, err := hex.DecodeString(req.Fingerprint)
	if err != nil || len(fp) != 32 || req.Size <= 0 || req.Filename == "" {
		jsonError(w, http.StatusBadRequest, "The upload request is missing its file details.")
		return
	}
	if req.Size > rawLimit {
		jsonError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("That file is larger than the site accepts (%s uncompressed). Clear the game's combat log between nights.", mib(rawLimit)))
		return
	}

	if have, err := a.store.ByFingerprint(r.Context(), uid, fp); err == nil {
		writeJSON(w, http.StatusOK, beginResponse{
			ID: have.ID, State: string(have.State), PiecesTotal: have.PiecesTotal,
			PiecesReceived: have.PiecesReceived, PieceBytes: fights.PieceBytes, URL: uploadURL(have.ID),
		})
		return
	} else if !errors.Is(err, fights.ErrNotFound) {
		a.deps.Logger.Error("look up upload by fingerprint", "error", err)
		jsonError(w, http.StatusInternalServerError, "The upload could not be started.")
		return
	}

	// The member's characters, as the card shows them, snapshotted so the
	// parser -- which has no session -- matches against the same list.
	profile, _ := platform.ProfileFrom(r.Context())
	chars := make([]fights.CharacterRef, 0, len(profile.Characters))
	for _, c := range profile.Characters {
		chars = append(chars, fights.CharacterRef{Name: c.Name, RealmSlug: c.RealmSlug})
	}

	u, err := a.store.Begin(r.Context(), fights.Upload{
		UserID: uid, BattleTag: tag, Filename: req.Filename, RawSize: req.Size, Fingerprint: fp,
		Characters: chars, PiecesTotal: int((req.Size + fights.PieceBytes - 1) / fights.PieceBytes),
	})
	if err != nil {
		a.deps.Logger.Error("begin upload", "error", err)
		jsonError(w, http.StatusInternalServerError, "The upload could not be started.")
		return
	}
	writeJSON(w, http.StatusCreated, beginResponse{
		ID: u.ID, State: string(u.State), PiecesTotal: u.PiecesTotal, PieceBytes: fights.PieceBytes,
	})
}

// piece appends one compressed piece.
func (a *App) piece(w http.ResponseWriter, r *http.Request) {
	if !a.guarded(w, r) {
		return
	}
	uid, _ := a.who(r)
	id, err1 := strconv.ParseInt(r.PathValue("id"), 10, 64)
	n, err2 := strconv.Atoi(r.PathValue("n"))
	if err1 != nil || err2 != nil || n < 0 {
		http.NotFound(w, r)
		return
	}

	mu := lockFor(id)
	mu.Lock()
	defer mu.Unlock()

	u, err := a.store.Upload(r.Context(), id, uid)
	if err != nil || u.State != fights.Receiving {
		http.NotFound(w, r)
		return
	}
	switch {
	case n < u.PiecesReceived:
		// A retry of a piece already held: nothing to do.
		writeJSON(w, http.StatusOK, map[string]int{"pieces_received": u.PiecesReceived})
		return
	case n > u.PiecesReceived || n >= u.PiecesTotal:
		writeJSON(w, http.StatusConflict, map[string]int{"pieces_received": u.PiecesReceived})
		return
	}

	// One piece is at most PieceBytes raw, and gzip never inflates by more
	// than a few bytes per block, so PieceBytes plus a margin is the cap.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, fights.PieceBytes+64*1024))
	if err != nil {
		jsonError(w, http.StatusBadRequest, "That piece is larger than a piece can be.")
		return
	}
	if !bytes.HasPrefix(body, gzipMagic) {
		jsonError(w, http.StatusBadRequest, "That piece is not compressed the way the uploader compresses it.")
		return
	}
	if u.StoredSize+int64(len(body)) > platform.UploadLimitBytes {
		_ = a.store.Fail(r.Context(), id, "the file is larger than the site accepts, even compressed; clear the game's combat log between nights")
		a.discard(id)
		jsonError(w, http.StatusRequestEntityTooLarge, "The file is larger than the site accepts, even compressed. Clear the game's combat log between nights.")
		return
	}

	if err := a.append(id, body); err != nil {
		a.deps.Logger.Error("write upload piece", "upload", id, "piece", n, "error", err)
		jsonError(w, http.StatusInternalServerError, "The piece could not be saved.")
		return
	}
	received, err := a.store.RecordPiece(r.Context(), id, n, int64(len(body)))
	if err != nil {
		a.deps.Logger.Error("record upload piece", "upload", id, "piece", n, "error", err)
		jsonError(w, http.StatusInternalServerError, "The piece could not be recorded.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"pieces_received": received})
}

// append adds a piece to the upload's file, creating it for the first.
func (a *App) append(id int64, body []byte) error {
	if err := os.MkdirAll(a.deps.Config.UploadDir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(a.path(id), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(body); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// finish moves a complete upload to the parser's queue.
func (a *App) finish(w http.ResponseWriter, r *http.Request) {
	if !a.guarded(w, r) {
		return
	}
	uid, _ := a.who(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	u, err := a.store.Upload(r.Context(), id, uid)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if u.State != fights.Receiving || u.PiecesReceived != u.PiecesTotal {
		writeJSON(w, http.StatusConflict, map[string]any{"pieces_received": u.PiecesReceived, "state": u.State})
		return
	}
	if err := a.store.Queue(r.Context(), id); err != nil {
		a.deps.Logger.Error("queue upload", "upload", id, "error", err)
		jsonError(w, http.StatusInternalServerError, "The upload could not be queued.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"state": string(fights.Queued), "url": uploadURL(id)})
}
