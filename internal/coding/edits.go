package coding

import (
	"bytes"
	"context"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Edit is encrypted at rest. It contains no access tokens or server paths.
// Only EditInfo is passed to the model; file content is read explicitly.
type Edit struct {
	Task          Task     `json:"task"`
	AccountID     int64    `json:"account_id"`
	Snapshot      Snapshot `json:"snapshot"`
	Review        Review   `json:"review"`
	Digest        string   `json:"digest"`
	FinishedEvent string   `json:"finished_event"`
	Commit        string   `json:"commit"`
}

type EditInfo struct {
	Task            Task     `json:"task"`
	Files           []string `json:"files,omitempty"`
	ChangedFiles    []string `json:"changed_files"`
	Digest          string   `json:"review_digest,omitempty"`
	ApprovalCommand string   `json:"approval_command,omitempty"`
	Commit          string   `json:"commit,omitempty"`
	Branch          string   `json:"branch"`
	Tests           string   `json:"tests"`
}

type EditStore interface {
	CreateEdit(context.Context, Edit) (Edit, error)
	GetEdit(context.Context, int64, string) (Edit, error)
	// Row lock serializes mutations and publishing, including across replicas.
	WithEdit(context.Context, int64, string, func(*Edit) error) error
}

type GitWorkspace interface {
	Clone(context.Context, string, Repository) (Snapshot, error)
	Push(context.Context, string, Review) (string, error)
}

// Task discovery lets a new turn recover persisted work after a timeout or a
// restart, even if the model did not get to deliver the task ID to Slack.
func (s *Service) EditTasks(ctx context.Context, i Identity) ([]Task, error) {
	lister, ok := s.edits.(interface {
		ListEditTasks(context.Context, Identity, int64) ([]Task, error)
	})
	if !ok {
		return nil, ErrUnavailable
	}
	account, err := s.Account(ctx, i)
	if err != nil {
		return nil, err
	}
	return lister.ListEditTasks(ctx, i, account.ID)
}

func (s *Service) EnableEditing(store EditStore, git GitWorkspace) error {
	if store == nil || git == nil {
		return ErrArguments
	}
	s.edits, s.git = store, git
	return nil
}

func (s *Service) EditingAvailable() bool { return s.edits != nil && s.git != nil }

func editInfo(e Edit, files bool) EditInfo {
	i := EditInfo{Task: e.Task, Digest: e.Digest, Commit: e.Commit, Branch: e.Review.Branch, Tests: "not_run"}
	if files {
		for _, f := range e.Snapshot.Files {
			i.Files = append(i.Files, f.Path)
		}
	}
	for _, f := range e.Review.Changes {
		i.ChangedFiles = append(i.ChangedFiles, f.Path)
	}
	sort.Strings(i.Files)
	sort.Strings(i.ChangedFiles)
	if e.Task.Status == "awaiting_user_test" {
		i.ApprovalCommand = approvalCommand(e)
	}
	return i
}

func approvalCommand(e Edit) string { return "pushla " + e.Task.ID + " " + e.Digest }

func boundReview(e Edit) bool {
	return e.Review.TaskID == e.Task.ID && e.Review.Repository == e.Task.Repository && e.Snapshot.Repository == e.Task.Repository && e.Review.BaseCommit == e.Snapshot.Commit && e.Review.BaseTree == e.Snapshot.Tree
}

func sameScope(a, b Identity) bool {
	return a.UserID == b.UserID && a.Team == b.Team && a.Channel == b.Channel && a.Thread == b.Thread
}

func (s *Service) editAccess(ctx context.Context, i Identity, e Edit) (string, error) {
	if !sameScope(i, e.Task.Identity) {
		return "", ErrNotFound
	}
	account, token, err := s.verifiedConnection(ctx, i)
	if err != nil {
		return "", err
	}
	if account.ID != e.AccountID {
		return "", ErrDenied
	}
	if checker, ok := s.provider.(interface {
		CheckRepository(context.Context, string, Account, Repository) error
	}); ok {
		if err := checker.CheckRepository(ctx, token, account, e.Task.Repository); err != nil {
			return "", err
		}
		return token, nil
	}
	repos, err := s.provider.Repositories(ctx, token)
	if err != nil {
		return "", err
	}
	for _, repo := range repos {
		if repo == e.Task.Repository {
			return token, nil
		}
	}
	return "", ErrDenied
}

func (s *Service) BeginEdit(ctx context.Context, i Identity, name string) (EditInfo, error) {
	if !s.EditingAvailable() {
		return EditInfo{}, ErrUnavailable
	}
	if !ValidRepository(name) || i.Event == "" || i.Channel == "" || i.Thread == "" || strings.TrimSpace(i.Request) == "" || len(i.Request) > 48000 {
		return EditInfo{}, ErrArguments
	}
	account, token, err := s.verifiedConnection(ctx, i)
	if err != nil {
		return EditInfo{}, err
	}
	repos, err := s.provider.Repositories(ctx, token)
	if err != nil {
		return EditInfo{}, err
	}
	var repo Repository
	for _, candidate := range repos {
		if strings.EqualFold(candidate.FullName, name) {
			repo = candidate
			break
		}
	}
	if repo.ID <= 0 {
		return EditInfo{}, ErrDenied
	}
	id, err := RandomID()
	if err != nil {
		return EditInfo{}, err
	}
	e, err := s.edits.CreateEdit(ctx, Edit{Task: Task{ID: id, Identity: i, Provider: "github", Repository: repo, Instruction: i.Request, Status: "cloning", CreatedAt: time.Now().UTC()}, AccountID: account.ID})
	if err != nil {
		return EditInfo{}, err
	}
	if e.Task.Repository != repo || e.AccountID != account.ID || !sameScope(i, e.Task.Identity) {
		return EditInfo{}, ErrDenied
	}
	err = s.edits.WithEdit(ctx, i.UserID, e.Task.ID, func(saved *Edit) error {
		if saved.Task.Status == "cloning" {
			snapshot, err := s.git.Clone(ctx, token, repo)
			if err != nil {
				return err
			}
			if snapshot.Repository != repo || !gitObjectID.MatchString(snapshot.Commit) || !gitObjectID.MatchString(snapshot.Tree) {
				return ErrUnavailable
			}
			saved.Snapshot = snapshot
			saved.Review = Review{TaskID: saved.Task.ID, Repository: repo, BaseCommit: snapshot.Commit, BaseTree: snapshot.Tree, Branch: "codex/nudgy-" + saved.Task.ID, Tests: "not_run"}
			saved.Task.Status = "editing"
		}
		e = *saved
		return nil
	})
	return editInfo(e, true), err
}

func (s *Service) EditStatus(ctx context.Context, i Identity, id string) (EditInfo, error) {
	e, err := s.loadEdit(ctx, i, id)
	return editInfo(e, true), err
}

func (s *Service) loadEdit(ctx context.Context, i Identity, id string) (Edit, error) {
	if !s.EditingAvailable() || !taskID.MatchString(id) {
		return Edit{}, ErrNotFound
	}
	e, err := s.edits.GetEdit(ctx, i.UserID, id)
	if err != nil {
		return Edit{}, err
	}
	if _, err := s.editAccess(ctx, i, e); err != nil {
		return Edit{}, err
	}
	return e, nil
}

// ReviewEdit is only for the handler after verifying the private, user-bound
// capability. It still checks the current GitHub identity and permissions.
func (s *Service) ReviewEdit(ctx context.Context, userID int64, id, digest string) (Edit, error) {
	if !s.EditingAvailable() || !taskID.MatchString(id) {
		return Edit{}, ErrNotFound
	}
	e, err := s.edits.GetEdit(ctx, userID, id)
	if err != nil {
		return Edit{}, err
	}
	if e.Digest == "" || e.Digest != digest || (e.Task.Status != "awaiting_user_test" && e.Task.Status != "pushed") {
		return Edit{}, ErrNotFound
	}
	actual, err := e.Review.Digest()
	if err != nil || actual != digest || !boundReview(e) {
		return Edit{}, ErrDenied
	}
	if _, err := s.editAccess(ctx, e.Task.Identity, e); err != nil {
		return Edit{}, err
	}
	return e, nil
}

func currentFile(e Edit, name string) (RepositoryFile, bool) {
	for _, c := range e.Review.Changes {
		if c.Path == name {
			return c.RepositoryFile, !c.Delete
		}
	}
	for _, f := range e.Snapshot.Files {
		if f.Path == name {
			return f, true
		}
	}
	return RepositoryFile{}, false
}

func (s *Service) ReadEditFile(ctx context.Context, i Identity, id, name string, start, count int) (string, error) {
	if !ValidFilePath(name) || start < 1 || count < 1 || count > 500 {
		return "", ErrArguments
	}
	e, err := s.loadEdit(ctx, i, id)
	if err != nil {
		return "", err
	}
	if e.Task.Status == "cancelled" || e.Task.Status == "cloning" {
		return "", ErrNotFound
	}
	f, ok := currentFile(e, name)
	if !ok {
		return "", ErrNotFound
	}
	if !utf8.Valid(f.Content) || bytes.IndexByte(f.Content, 0) >= 0 {
		return "", errors.New("ikili dosya metin olarak okunamaz")
	}
	lines := strings.Split(string(f.Content), "\n")
	if start > len(lines) {
		return "", ErrArguments
	}
	end := min(start-1+count, len(lines))
	result := strings.Join(lines[start-1:end], "\n")
	if len(result) > 128<<10 {
		return "", errors.New("daha az satır oku; dosya bölümü çok büyük")
	}
	return result, nil
}

func (s *Service) WriteEditFile(ctx context.Context, i Identity, id, name, content string, remove bool) (EditInfo, error) {
	if !s.EditingAvailable() || !taskID.MatchString(id) || !ValidFilePath(name) || !utf8.ValidString(content) || strings.ContainsRune(content, 0) || len(content) > 1<<20 || (remove && content != "") {
		return EditInfo{}, ErrArguments
	}
	// Workflows require additional GitHub permissions, intentionally not requested.
	if strings.HasPrefix(strings.ToLower(name), ".github/workflows/") {
		return EditInfo{}, errors.New("GitHub Actions dosyalarını değiştirme yetkisi bu bağlantıda etkin değil")
	}
	var result EditInfo
	err := s.edits.WithEdit(ctx, i.UserID, id, func(e *Edit) error {
		if _, err := s.editAccess(ctx, i, *e); err != nil {
			return err
		}
		if e.Task.Status != "editing" && e.Task.Status != "awaiting_user_test" {
			return errors.New("bu görev düzenlenemez")
		}
		original, exists := RepositoryFile{}, false
		for _, f := range e.Snapshot.Files {
			if f.Path == name {
				original, exists = f, true
				break
			}
		}
		mode := "100644"
		if exists {
			mode = original.Mode
		}
		changes := make([]FileChange, 0, len(e.Review.Changes)+1)
		for _, c := range e.Review.Changes {
			if c.Path != name {
				changes = append(changes, c)
			}
		}
		if (remove && exists) || (!remove && (!exists || !bytes.Equal(original.Content, []byte(content)))) {
			changes = append(changes, FileChange{RepositoryFile: RepositoryFile{Path: name, Mode: mode, Content: []byte(content)}, Delete: remove})
		}
		candidate := e.Review
		candidate.Changes = changes
		candidate.CreatedAt = time.Now().UTC()
		if len(changes) > 0 {
			if err := candidate.Validate(); err != nil {
				return err
			}
		}
		// Reject file/directory collisions with unchanged source files as well.
		for _, f := range e.Snapshot.Files {
			deleted := false
			for _, c := range changes {
				if c.Path == f.Path && c.Delete {
					deleted = true
				}
			}
			if deleted {
				continue
			}
			for _, c := range changes {
				if !c.Delete && c.Path != f.Path && (strings.HasPrefix(c.Path, f.Path+"/") || strings.HasPrefix(f.Path, c.Path+"/")) {
					return ErrArguments
				}
			}
		}
		e.Review = candidate
		e.Digest = ""
		e.FinishedEvent = ""
		e.Task.Status = "editing"
		result = editInfo(*e, false)
		return nil
	})
	return result, err
}

func (s *Service) FinishEdit(ctx context.Context, i Identity, id, summary string) (EditInfo, error) {
	if !s.EditingAvailable() || !taskID.MatchString(id) || strings.TrimSpace(summary) == "" || len(summary) > 16000 {
		return EditInfo{}, ErrArguments
	}
	var result EditInfo
	err := s.edits.WithEdit(ctx, i.UserID, id, func(e *Edit) error {
		if _, err := s.editAccess(ctx, i, *e); err != nil {
			return err
		}
		if e.Task.Status == "awaiting_user_test" {
			result = editInfo(*e, false)
			return nil
		}
		if e.Task.Status != "editing" {
			return ErrArguments
		}
		e.Review.Summary = summary
		e.Review.CreatedAt = time.Now().UTC()
		digest, err := e.Review.Digest()
		if err != nil {
			return err
		}
		e.Digest = digest
		e.FinishedEvent = i.Event
		e.Task.Status = "awaiting_user_test"
		result = editInfo(*e, false)
		return nil
	})
	return result, err
}

func (s *Service) PushEdit(ctx context.Context, i Identity, id string) (EditInfo, error) {
	return s.pushEdit(ctx, i, id, func(e Edit) bool {
		return i.Event != "" && i.Event != e.FinishedEvent && i.Event != e.Task.Identity.Event && strings.TrimSpace(i.Request) == approvalCommand(e)
	})
}

// PushEditFromButton is only called by the signature-verified Slack interaction
// handler after checking the owner-bound button capability. Never expose it as
// a model tool, an unsigned HTTP route, or convert model text into a click.
func (s *Service) PushEditFromButton(ctx context.Context, i Identity, id, digest string) (EditInfo, error) {
	return s.pushEdit(ctx, i, id, func(e Edit) bool { return strings.HasPrefix(i.Event, "button:") && digest != "" && digest == e.Digest })
}

func (s *Service) pushEdit(ctx context.Context, i Identity, id string, approved func(Edit) bool) (EditInfo, error) {
	if !s.EditingAvailable() || !taskID.MatchString(id) {
		return EditInfo{}, ErrNotFound
	}
	var result EditInfo
	err := s.edits.WithEdit(ctx, i.UserID, id, func(e *Edit) error {
		if !sameScope(i, e.Task.Identity) {
			return ErrNotFound
		}
		// Approval is the entire current, signed Slack message, never model args,
		// history, repository instructions or the message that prepared the code.
		if e.Digest == "" || !approved(*e) {
			return errors.New("push için güncel inceleme mesajındaki Pushla butonuna görevin sahibi basmalı")
		}
		digest, err := e.Review.Digest()
		if err != nil || digest != e.Digest || !boundReview(*e) {
			return ErrDenied
		}
		token, err := s.editAccess(ctx, i, *e)
		if err != nil {
			return err
		}
		if e.Task.Status == "pushed" {
			result = editInfo(*e, false)
			return nil
		}
		if e.Task.Status != "awaiting_user_test" {
			return ErrDenied
		}
		commit, err := s.git.Push(ctx, token, e.Review)
		if err != nil {
			return err
		}
		e.Commit = commit
		e.Task.Status = "pushed"
		result = editInfo(*e, false)
		return nil
	})
	return result, err
}

func (s *Service) CancelEdit(ctx context.Context, i Identity, id string) (EditInfo, error) {
	if !s.EditingAvailable() || !taskID.MatchString(id) {
		return EditInfo{}, ErrNotFound
	}
	var result EditInfo
	err := s.edits.WithEdit(ctx, i.UserID, id, func(e *Edit) error {
		if err := s.AuthorizeIdentity(ctx, i); err != nil {
			return err
		}
		if !sameScope(i, e.Task.Identity) {
			return ErrNotFound
		}
		if e.Task.Status == "pushed" {
			return errors.New("görev zaten pushlandı; geri alma ayrı bir değişiklik gerektirir")
		}
		e.Task.Status = "cancelled"
		e.Digest = ""
		e.Snapshot.Files = nil
		e.Review.Changes = nil
		result = editInfo(*e, false)
		return nil
	})
	return result, err
}

func EditedFiles(e Edit) []RepositoryFile {
	files := make(map[string]RepositoryFile)
	for _, f := range e.Snapshot.Files {
		files[f.Path] = f
	}
	for _, c := range e.Review.Changes {
		if c.Delete {
			delete(files, c.Path)
		} else {
			files[c.Path] = c.RepositoryFile
		}
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]RepositoryFile, 0, len(names))
	for _, name := range names {
		result = append(result, files[name])
	}
	return result
}
