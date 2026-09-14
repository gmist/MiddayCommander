//go:build windows

package remote

import (
	"io"
	"os"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// agentPipe is where the Windows OpenSSH agent listens: there is no
// SSH_AUTH_SOCK, and the pipe opens like a file.
const agentPipe = `\\.\pipe\openssh-ssh-agent`

func agentSigners() ([]ssh.Signer, io.Closer, error) {
	f, err := os.OpenFile(agentPipe, os.O_RDWR, 0)
	if err != nil {
		return nil, nil, err
	}

	signers, err := agent.NewClient(f).Signers()
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	return signers, f, nil
}
