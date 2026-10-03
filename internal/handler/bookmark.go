package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"nudge/internal/domain"
	"nudge/internal/service"
)

type BookmarkHandler struct {
	service service.BookmarkService
}

func NewBookmarkHandler(bookmarkService service.BookmarkService) *BookmarkHandler {
	return &BookmarkHandler{service: bookmarkService}
}

func (h *BookmarkHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/bookmarks", h.create)
	mux.HandleFunc("GET /api/v1/bookmarks", h.list)
	mux.HandleFunc("GET /api/v1/bookmarks/{id}", h.getByID)
	mux.HandleFunc("POST /api/v1/bookmarks/{id}/read", h.markRead)
}

type createBookmarkRequest struct {
	RaindropBookmarkID int64  `json:"raindrop_bookmark_id"`
	Title              string `json:"title"`
	URL                string `json:"url"`
	Summary            string `json:"summary"`
	SavedAt            string `json:"saved_at"`
}

func (h *BookmarkHandler) create(w http.ResponseWriter, r *http.Request) {
	userID, err := userIDFromHeader(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var request createBookmarkRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	savedAt, err := time.Parse(time.RFC3339, request.SavedAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, "saved_at must be an RFC3339 timestamp")
		return
	}

	bookmark, err := h.service.Create(r.Context(), domain.Bookmark{
		UserID:             userID,
		RaindropBookmarkID: request.RaindropBookmarkID,
		Title:              request.Title,
		URL:                request.URL,
		Summary:            request.Summary,
		SavedAt:            savedAt,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, bookmark)
}

func (h *BookmarkHandler) list(w http.ResponseWriter, r *http.Request) {
	userID, err := userIDFromHeader(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	from, to, err := dateRangeFromQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var bookmarks []domain.Bookmark
	if r.URL.Query().Get("unread") == "true" {
		bookmarks, err = h.service.ListUnreadBetween(r.Context(), userID, from, to)
	} else {
		bookmarks, err = h.service.ListByDate(r.Context(), userID, from, to)
	}
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, bookmarks)
}

func (h *BookmarkHandler) getByID(w http.ResponseWriter, r *http.Request) {
	userID, err := userIDFromHeader(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	bookmarkID, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid bookmark ID")
		return
	}

	bookmark, err := h.service.GetByID(r.Context(), userID, bookmarkID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, bookmark)
}

func (h *BookmarkHandler) markRead(w http.ResponseWriter, r *http.Request) {
	userID, err := userIDFromHeader(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	bookmarkID, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid bookmark ID")
		return
	}

	if err := h.service.MarkRead(r.Context(), userID, bookmarkID); err != nil {
		writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func userIDFromHeader(r *http.Request) (int64, error) {
	value := strings.TrimSpace(r.Header.Get("X-User-ID"))
	if value == "" {
		return 0, errors.New("X-User-ID header is required")
	}
	userID, err := strconv.ParseInt(value, 10, 64)
	if err != nil || userID <= 0 {
		return 0, errors.New("X-User-ID must be a positive integer")
	}
	return userID, nil
}

func pathID(r *http.Request) (int64, error) {
	return strconv.ParseInt(r.PathValue("id"), 10, 64)
}

func dateRangeFromQuery(r *http.Request) (time.Time, time.Time, error) {
	from, err := time.Parse(time.RFC3339, r.URL.Query().Get("from"))
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("from must be an RFC3339 timestamp")
	}
	to, err := time.Parse(time.RFC3339, r.URL.Query().Get("to"))
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("to must be an RFC3339 timestamp")
	}
	if !from.Before(to) {
		return time.Time{}, time.Time{}, errors.New("from must be before to")
	}
	return from, to, nil
}

func writeServiceError(w http.ResponseWriter, err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "bookmark not found")
		return
	}
	if errors.Is(err, service.ErrInvalidBookmark) || errors.Is(err, service.ErrInvalidDateRange) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeError(w, http.StatusInternalServerError, "internal server error")
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
