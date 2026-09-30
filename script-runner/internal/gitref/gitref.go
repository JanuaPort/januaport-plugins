// Package gitref prüft alles, was aus Git-Richtung kommt, bevor es einen
// git-Aufruf oder einen Pfad erreicht: Commit-Hashes (V2), Repo-URLs (M7),
// Pin-Pfade und Pin-Namen (Nachtrag 2a). Die Prüfer sind Allowlists.
package gitref

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

var (
	commitRe  = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
	nameRe    = regexp.MustCompile(`^[a-z0-9_]{1,48}$`)
	hostRe    = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,252})$`)
	userRe    = regexp.MustCompile(`^[A-Za-z0-9._][A-Za-z0-9._-]{0,63}$`)
	portRe    = regexp.MustCompile(`^[0-9]{1,5}$`)
	repoPath  = regexp.MustCompile(`^[A-Za-z0-9._~][A-Za-z0-9._/~-]*$`)
	scpLikeRe = regexp.MustCompile(`^([^@:/]+)@([^:/]+):(.+)$`)
)

// ValidCommit: genau 40 (SHA-1) oder 64 (SHA-256) kleine Hex-Zeichen.
func ValidCommit(s string) bool { return commitRe.MatchString(s) }

// ValidPinName: [a-z0-9_]{1,48} (Vertrag §2).
func ValidPinName(s string) bool { return nameRe.MatchString(s) }

// ValidPinPath: relativ, ohne leere, `.`- oder `..`-Bestandteile, ohne
// `.git` in beliebiger Schreibung, ohne Backslash und Steuerzeichen.
func ValidPinPath(p string) bool {
	if p == "" || strings.HasPrefix(p, "-") {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if !validSegment(seg) {
			return false
		}
	}
	return true
}

// ValidSegment prüft einen einzelnen Namen (Baum-Eintrag oder
// Pfadbestandteil).
func ValidSegment(seg string) bool { return validSegment(seg) }

func validSegment(seg string) bool {
	if seg == "" || seg == "." || seg == ".." || strings.EqualFold(seg, ".git") {
		return false
	}
	for _, r := range seg {
		if r < 0x20 || r == 0x7f || r == '\\' || r == '/' {
			return false
		}
	}
	return true
}

// CheckRepoURL lässt nur ssh zu: `ssh://[user@]host[:port]/pfad` oder die
// scp-Form `user@host:pfad`. Alles, was git als Option, lokalen Pfad oder
// Hilfsprogramm (`ext::`, `fd::`) lesen könnte, fällt durch.
func CheckRepoURL(s string) error {
	if s == "" || strings.HasPrefix(s, "-") || strings.Contains(s, "::") {
		return errors.New("gitref: keine ssh-URL")
	}
	for _, r := range s {
		if r <= 0x20 || r == 0x7f {
			return errors.New("gitref: Leer- oder Steuerzeichen in der URL")
		}
	}
	if strings.HasPrefix(s, "ssh://") {
		return checkSSHURL(s)
	}
	if strings.Contains(s, "://") {
		return errors.New("gitref: nur ssh erlaubt")
	}
	m := scpLikeRe.FindStringSubmatch(s)
	if m == nil {
		return errors.New("gitref: keine ssh-URL")
	}
	return checkParts(m[1], m[2], "", m[3])
}

func checkSSHURL(s string) error {
	u, err := url.Parse(s)
	if err != nil || u.Opaque != "" || u.RawQuery != "" || u.Fragment != "" || strings.Contains(s, "?") || strings.Contains(s, "#") {
		return errors.New("gitref: ungültige ssh-URL")
	}
	user := ""
	if u.User != nil {
		if _, hasPw := u.User.Password(); hasPw {
			return errors.New("gitref: Passwort in der URL")
		}
		user = u.User.Username()
	}
	return checkParts(user, u.Hostname(), u.Port(), strings.TrimPrefix(u.EscapedPath(), "/"))
}

func checkParts(user, host, port, path string) error {
	if user != "" && !userRe.MatchString(user) {
		return errors.New("gitref: ungültiger Nutzer")
	}
	if !hostRe.MatchString(host) {
		return errors.New("gitref: ungültiger Host")
	}
	if port != "" && !portRe.MatchString(port) {
		return errors.New("gitref: ungültiger Port")
	}
	if !repoPath.MatchString(path) {
		return errors.New("gitref: ungültiger Repo-Pfad")
	}
	for _, seg := range strings.Split(path, "/") {
		if seg == ".." {
			return errors.New("gitref: .. im Repo-Pfad")
		}
	}
	return nil
}
