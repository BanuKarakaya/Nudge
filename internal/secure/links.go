package secure

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
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
	UserID  int64  `json:"u"`
	Purpose string `json:"p"`
	Expires int64  `json:"e"`
}

func (s LinkSigner) Token(userID int64, purpose string, expires time.Time) (string, error) {
	if len(s.key) != 32 || userID <= 0 || (purpose != "feedbin" && purpose != "raindrop") {
		return "", errors.New("invalid account link configuration")
	}
	raw, _ := json.Marshal(linkClaims{userID, purpose, expires.Unix()})
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
	token, err := s.Token(userID, purpose, now.Add(8*24*time.Hour))
	if err != nil {
		return "", err
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
		return "", errors.New("invalid public URL")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/" + purpose + "/install"
	u.RawQuery = url.Values{"user_id": {strconv.FormatInt(userID, 10)}, "token": {token}}.Encode()
	u.Fragment = ""
	return u.String(), nil
}
