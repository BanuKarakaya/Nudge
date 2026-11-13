package secure

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// LinkSigner issues bearer capabilities for one user's account connection.
// Use a dedicated key, independent of the credential encryption key.
type LinkSigner struct{ key []byte }

func NewLinkSigner(encodedKey string) (LinkSigner, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedKey))
	if err != nil || len(key) != 32 {
		return LinkSigner{}, errors.New("link signing key must be base64 encoded and exactly 32 bytes")
	}
	return LinkSigner{key: key}, nil
}

type linkClaims struct {
	Nonce   string `json:"n,omitempty"`
	UserID  int64  `json:"u"`
	Purpose string `json:"p"`
	Expires int64  `json:"e"`
}

var codingReviewPurpose = regexp.MustCompile(`^(coding|gitlab)-review:[0-9a-f]{32}:[0-9a-f]{64}$`)
var codingPushPurpose = regexp.MustCompile(`^coding-push:[0-9a-f]{32}:[0-9a-f]{64}$`)

func (s LinkSigner) Token(userID int64, purpose string, expires time.Time) (string, error) {
	if len(s.key) != 32 || userID <= 0 || (purpose != "workspace" && purpose != "feedbin" && purpose != "raindrop" && purpose != "github" && purpose != "gitlab" && purpose != "jira" && purpose != "trello" && !codingReviewPurpose.MatchString(purpose) && !codingPushPurpose.MatchString(purpose)) {
		return "", errors.New("invalid account link configuration")
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	raw, _ := json.Marshal(linkClaims{UserID: userID, Purpose: purpose, Expires: expires.Unix(), Nonce: base64.RawURLEncoding.EncodeToString(nonce)})
	body := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(body))
	return body + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (s LinkSigner) Verify(token string, userID int64, purpose string, now time.Time) bool {
	if len(s.key) != 32 || len(token) > 1024 || userID <= 0 {
		return false
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return false
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(parts[0]))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}
	var claims linkClaims
	return json.Unmarshal(raw, &claims) == nil && claims.UserID == userID && claims.Purpose == purpose && now.Unix() < claims.Expires
}

func (s LinkSigner) URL(base string, userID int64, purpose string, now time.Time) (string, error) {
	lifetime := 20 * time.Minute
	token, err := s.Token(userID, purpose, now.Add(lifetime))
	if err != nil {
		return "", err
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
		return "", errors.New("invalid public URL")
	}
	pathPurpose := purpose
	if purpose == "workspace" {
		pathPurpose = "dashboard"
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/" + pathPurpose + "/install"
	u.RawQuery = url.Values{"user_id": {strconv.FormatInt(userID, 10)}, "token": {token}}.Encode()
	u.Fragment = ""
	return u.String(), nil
}
