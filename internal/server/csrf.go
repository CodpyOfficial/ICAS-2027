package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
)

const (
	csrfCookieName = "icas_csrf"
	csrfFieldName  = "csrf_token"
	csrfTokenBytes = 32
)

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}

func validTokenFormat(tok string) bool {
	if len(tok) != csrfTokenBytes*2 {
		return false
	}
	_, err := hex.DecodeString(tok)
	return err == nil
}

// csrfToken returns the browser's CSRF token, issuing a new cookie when the
// request has none. Forms echo the token in a hidden field (double-submit
// cookie); a cross-site page can neither read the cookie nor set it.
func csrfToken(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie(csrfCookieName); err == nil && validTokenFormat(c.Value) {
		return c.Value
	}
	tok := randomHex(csrfTokenBytes)
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    tok,
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})
	return tok
}

// validCSRF reports whether a POST carries a form token matching its cookie.
// The form must already be parsed.
func validCSRF(r *http.Request) bool {
	c, err := r.Cookie(csrfCookieName)
	if err != nil || !validTokenFormat(c.Value) {
		return false
	}
	sent := r.PostFormValue(csrfFieldName)
	return subtle.ConstantTimeCompare([]byte(c.Value), []byte(sent)) == 1
}
