package handler

import (
	"archive/zip"
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"nudge/internal/coding"
)

//go:embed coding_review.html
var codingReviewHTML string
var codingReviewTemplate = template.Must(template.New("review").Parse(codingReviewHTML))

func reviewPurpose(id, digest string) string { return "coding-review:" + id + ":" + digest }

func (h *CodingHandler) sendEditReview(ctx context.Context, identity coding.Identity, info coding.EditInfo) error {
	installation, err := h.installations.GetByUserID(ctx, identity.UserID)
	if err != nil {
		return err
	}
	if installation.TeamID != identity.Team {
		return coding.ErrDenied
	}
	userID, err := h.installations.GetUserIDBySlackUserID(ctx, identity.Team, installation.SlackUserID)
	if err != nil || userID != identity.UserID {
		return coding.ErrDenied
	}
	token, err := h.links.Token(identity.UserID, reviewPurpose(info.Task.ID, info.Digest), time.Now().Add(24*time.Hour))
	if err != nil {
		return err
	}
	query := url.Values{"user_id": {strconv.FormatInt(identity.UserID, 10)}, "task_id": {info.Task.ID}, "digest": {info.Digest}, "token": {token}}
	link := h.publicURL + "/github/review?" + query.Encode()
	channel, err := h.client.OpenDirectMessage(ctx, installation.BotToken, installation.SlackUserID)
	if err != nil {
		return err
	}
	message := "Kod değişiklikleri hazır: " + info.Task.Repository.FullName + "\nOtomatik test çalıştırmadım ve GitHub’a henüz pushlamadım. Özel sayfadan değişiklikleri inceleyip çalışma kopyasını indirerek test edebilirsin:\n" + link + "\nTestin tamamlanınca görevi istediğin konuşmadaki mesajın altında bulunan Pushla butonuna bas. Buton yalnızca senin bu sürümü onaylamanı sağlar. Link 24 saat geçerli; paylaşma."
	if err := h.client.PostMessage(ctx, installation.BotToken, channel, message); err != nil {
		return errors.New("kod kaydedildi; özel inceleme mesajı gönderilemedi, bitirme işlemini tekrar dene")
	}
	return nil
}

type reviewFileView struct {
	Path, Before, After string
	Delete              bool
}
type reviewPageView struct {
	Edit         coding.Edit
	Download     string
	XcodeProject string
	Files        []reviewFileView
	Pushed       bool
}

func (h *CodingHandler) review(w http.ResponseWriter, r *http.Request) {
	privatePageHeaders(w)
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src 'self'; frame-ancestors 'none'; form-action 'none'; base-uri 'none'")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	q := r.URL.Query()
	id, digest := q.Get("task_id"), q.Get("digest")
	userID, err := strconv.ParseInt(q.Get("user_id"), 10, 64)
	if err != nil || !h.links.Verify(q.Get("token"), userID, reviewPurpose(id, digest), time.Now()) {
		renderGitHubConnectionPage(w, http.StatusUnauthorized, githubPageData{Title: "İnceleme bağlantısı geçersiz.", Description: "Slack’te Nudgy’den bu görev için yeni bir inceleme bağlantısı iste."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	e, err := h.service.ReviewEdit(ctx, userID, id, digest)
	if err != nil {
		renderGitHubConnectionPage(w, http.StatusForbidden, githubPageData{Title: "Bu sürüm artık incelenemiyor.", Description: "Kod değişmiş, görev iptal edilmiş veya GitHub erişimin değişmiş olabilir. Slack’te Nudgy’den güncel inceleme bağlantısını iste."})
		return
	}
	if q.Get("download") == "zip" {
		data, err := reviewArchive(e)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "archive unavailable")
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="nudgy-workspace.zip"`)
		w.Write(data)
		return
	}
	q.Set("download", "zip")
	data := reviewPageView{Edit: e, Download: "/github/review?" + q.Encode(), XcodeProject: xcodeProject(e), Pushed: e.Task.Status == "pushed"}
	before := make(map[string][]byte)
	for _, f := range e.Snapshot.Files {
		before[f.Path] = f.Content
	}
	for _, change := range e.Review.Changes {
		data.Files = append(data.Files, reviewFileView{Path: change.Path, Before: reviewText(before[change.Path]), After: reviewText(change.Content), Delete: change.Delete})
	}
	var body bytes.Buffer
	if err := codingReviewTemplate.Execute(&body, data); err != nil {
		writeError(w, http.StatusInternalServerError, "review unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(body.Bytes())
}

func reviewText(b []byte) string {
	if !utf8.Valid(b) || bytes.IndexByte(b, 0) >= 0 {
		return "İkili dosya; tam içeriği çalışma kopyasından incele."
	}
	if len(b) > 128<<10 {
		return "Dosya uzun olduğu için bu görünüm kısaltıldı. Tam içeriği çalışma kopyasından incele.\n\n" + string(bytes.ToValidUTF8(b[:128<<10], []byte("�")))
	}
	return string(b)
}

func reviewArchive(e coding.Edit) ([]byte, error) {
	var out bytes.Buffer
	archive := zip.NewWriter(&out)
	meta, err := archive.Create("NUDGY-REVIEW.txt")
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(meta, "Repo: %s\nBase: %s\nBranch: %s\nReview: %s\n\n%s\n\nOtomatik test çalıştırılmadı. workspace/ klasörü düzenlenmiş kaynak kodudur; .git içermez. Kendi test ortamında bu klasörü açıp test et. GitHub'a yalnızca Slack'teki açık onayından sonra push yapılır.\n", e.Task.Repository.FullName, e.Review.BaseCommit, e.Review.Branch, e.Digest, e.Review.Summary)
	for _, f := range coding.EditedFiles(e) {
		if !coding.ValidFilePath(f.Path) || strings.Contains(f.Path, "\x00") {
			return nil, coding.ErrArguments
		}
		mode := os.FileMode(0644)
		if f.Mode == "100755" {
			mode = 0755
		}
		header := &zip.FileHeader{Name: "workspace/" + f.Path, Method: zip.Deflate}
		header.SetMode(mode)
		file, err := archive.CreateHeader(header)
		if err != nil {
			return nil, err
		}
		if _, err := file.Write(f.Content); err != nil {
			return nil, err
		}
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func xcodeProject(e coding.Edit) string {
	var project string
	for _, f := range coding.EditedFiles(e) {
		if strings.HasSuffix(f.Path, ".xcworkspace/contents.xcworkspacedata") && !strings.Contains(f.Path, ".xcodeproj/") {
			return "workspace/" + strings.TrimSuffix(f.Path, "/contents.xcworkspacedata")
		}
		if project == "" && strings.HasSuffix(f.Path, ".xcodeproj/project.pbxproj") {
			project = "workspace/" + strings.TrimSuffix(f.Path, "/project.pbxproj")
		}
	}
	return project
}
