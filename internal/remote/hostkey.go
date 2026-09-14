package remote

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// UnknownHostError means the user must confirm the fingerprint first.
type UnknownHostError struct {
	Host        string
	Fingerprint string
	KeyType     string
}

func (e *UnknownHostError) Error() string {
	return fmt.Sprintf("unknown host %s (%s %s)", e.Host, e.KeyType, e.Fingerprint)
}

// ChangedHostKeyError is never offered as a prompt: it is what interception
// looks like, and is resolved by editing known_hosts.
type ChangedHostKeyError struct {
	Host        string
	Fingerprint string
	KeyType     string
}

func (e *ChangedHostKeyError) Error() string {
	return fmt.Sprintf(
		"host key for %s has changed (now %s %s) — if this is not expected, "+
			"someone may be intercepting the connection; remove the old entry "+
			"from %s once you have verified the new key",
		e.Host, e.KeyType, e.Fingerprint, KnownHostsPath())
}

func KnownHostsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "known_hosts"
	}
	return filepath.Join(home, ".ssh", "known_hosts")
}

// hostKeyCallback separates an unknown key from a changed one, so the UI can
// ask about the first and refuse the second.
func hostKeyCallback() (ssh.HostKeyCallback, error) {
	path := KnownHostsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	// knownhosts.New fails on a missing file.
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, err
		}
		_ = f.Close()
	}

	inner, err := knownhosts.New(path)
	if err != nil {
		return nil, err
	}

	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := inner(hostname, remote, key)
		if err == nil {
			return nil
		}

		var keyErr *knownhosts.KeyError
		if errors.As(err, &keyErr) {
			details := struct {
				host        string
				fingerprint string
				keyType     string
			}{
				host:        knownhosts.Normalize(hostname),
				fingerprint: ssh.FingerprintSHA256(key),
				keyType:     key.Type(),
			}
			// An empty Want means the host is not on file; a populated one
			// means a different key is already recorded.
			if len(keyErr.Want) == 0 {
				return &UnknownHostError{
					Host:        details.host,
					Fingerprint: details.fingerprint,
					KeyType:     details.keyType,
				}
			}
			return &ChangedHostKeyError{
				Host:        details.host,
				Fingerprint: details.fingerprint,
				KeyType:     details.keyType,
			}
		}
		return err
	}, nil
}

// trustHost records a key, only after the user confirmed its fingerprint.
func trustHost(hostname string, key ssh.PublicKey) error {
	path := KnownHostsPath()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	line := knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key)
	_, err = fmt.Fprintln(f, line)
	return err
}

// pinnedHostKey accepts only the key whose fingerprint the user confirmed.
// Taking whatever the retry presents would reopen the gap the prompt closes.
func pinnedHostKey(hostname, wantFingerprint string) (ssh.HostKeyCallback, func() error) {
	var accepted ssh.PublicKey

	cb := func(_ string, _ net.Addr, key ssh.PublicKey) error {
		got := ssh.FingerprintSHA256(key)
		if got != wantFingerprint {
			return fmt.Errorf(
				"host key changed between the prompt and the connection (expected %s, got %s)",
				wantFingerprint, got)
		}
		accepted = key
		return nil
	}

	save := func() error {
		if accepted == nil {
			return errors.New("no host key was presented")
		}
		return trustHost(hostname, accepted)
	}
	return cb, save
}
