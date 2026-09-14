//go:build !windows

package remote

import (
	"errors"
	"io"
	"net"
	"os"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// agentSigners returns the keys ssh-agent holds. The closer must stay open
// until authentication finishes: the signers sign through this connection.
func agentSigners() ([]ssh.Signer, io.Closer, error) {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil, nil, errors.New("no ssh-agent: SSH_AUTH_SOCK is not set")
	}

	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil, nil, err
	}

	signers, err := agent.NewClient(conn).Signers()
	if err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	return signers, conn, nil
}
