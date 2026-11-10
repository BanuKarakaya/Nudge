package coding

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"unicode"
)

// CommitAuthor is obtained from the authenticated provider account, never a model
// argument. It is frozen in the review so retries produce the same commit.
type CommitAuthor struct {
	AccountID int64  `json:"account_id"`
	Name      string `json:"name"`
	Email     string `json:"email"`
}

func (a CommitAuthor) valid() bool {
	if a.AccountID <= 0 || a.Name == "" || len(a.Name) > 200 || len(a.Email) > 320 {
		return false
	}
	for _, c := range a.Name {
		if unicode.IsControl(c) || c == '<' || c == '>' {
			return false
		}
	}
	address, err := mail.ParseAddress(a.Email)
	return err == nil && address.Address == a.Email && !strings.ContainsAny(a.Email, "\r\n\x00<>")
}

type commitAuthorProvider interface {
	CommitAuthor(context.Context, string, Account) (CommitAuthor, error)
}

func (g GitHub) CommitAuthor(_ context.Context, _ string, a Account) (CommitAuthor, error) {
	if a.ID <= 0 || !githubLogin.MatchString(a.Login) {
		return CommitAuthor{}, ErrConnect
	}
	// GitHub's ID-based no-reply address preserves privacy and account attribution.
	return CommitAuthor{AccountID: a.ID, Name: a.Login, Email: fmt.Sprintf("%d+%s@users.noreply.github.com", a.ID, a.Login)}, nil
}
func (g GitLab) CommitAuthor(ctx context.Context, token string, a Account) (CommitAuthor, error) {
	var user struct {
		ID    int64  `json:"id"`
		Login string `json:"username"`
		Email string `json:"commit_email"`
	}
	if err := g.request(ctx, "https://gitlab.com/api/v4/user", token, nil, &user); err != nil {
		return CommitAuthor{}, err
	}
	author := CommitAuthor{AccountID: user.ID, Name: user.Login, Email: user.Email}
	if user.ID != a.ID || user.Login != a.Login {
		return CommitAuthor{}, ErrConnect
	}
	if !author.valid() {
		return CommitAuthor{}, errors.New("GitLab commit e-posta adresi doğrulanamadı; GitLab profilindeki commit e-posta ayarını kontrol et")
	}
	return author, nil
}
