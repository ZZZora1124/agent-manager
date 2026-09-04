package ssh

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	defaultPort = "22"
	dialTimeout = 10 * time.Second
)

// Client wraps an SSH connection to a remote host.
type Client struct {
	client *ssh.Client
	host   string
}

// Dial connects to the remote host using password authentication.
// host can be "ip" or "ip:port"; defaults to port 22.
func Dial(host, user, password string) (*Client, error) {
	addr := host
	if _, _, err := net.SplitHostPort(host); err != nil {
		addr = net.JoinHostPort(host, defaultPort)
	}

	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         dialTimeout,
	}

	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return nil, fmt.Errorf("ssh dial %s: %w", addr, err)
	}
	return &Client{client: client, host: addr}, nil
}

// Close closes the underlying SSH connection.
func (c *Client) Close() error {
	if c.client != nil {
		return c.client.Close()
	}
	return nil
}

// Run executes a command and returns its combined stdout output.
func (c *Client) Run(cmd string) (string, error) {
	session, err := c.client.NewSession()
	if err != nil {
		return "", fmt.Errorf("new session: %w", err)
	}
	defer session.Close()

	var stdout bytes.Buffer
	session.Stdout = &stdout

	if err := session.Run(cmd); err != nil {
		return stdout.String(), fmt.Errorf("run %q on %s: %w", cmd, c.host, err)
	}
	return stdout.String(), nil
}

// RunStream executes a command and streams its stdout to the provided writer.
// If w is an *io.PipeWriter, it will be closed (with error if any) when the command finishes,
// signaling EOF to the reader side.
func (c *Client) RunStream(cmd string, w io.Writer) error {
	session, err := c.client.NewSession()
	if err != nil {
		return fmt.Errorf("new session: %w", err)
	}
	defer session.Close()

	session.Stdout = w
	runErr := session.Run(cmd)

	// Close pipe writer to signal EOF to the reader side.
	if pw, ok := w.(*io.PipeWriter); ok {
		if runErr != nil {
			_ = pw.CloseWithError(runErr)
		} else {
			_ = pw.Close()
		}
	}
	return runErr
}

// RunWithStdin executes a command, feeding stdin from the provided reader.
func (c *Client) RunWithStdin(cmd string, r io.Reader) error {
	session, err := c.client.NewSession()
	if err != nil {
		return fmt.Errorf("new session: %w", err)
	}
	defer session.Close()

	session.Stdin = r

	if err := session.Run(cmd); err != nil {
		return fmt.Errorf("run %q on %s: %w", cmd, c.host, err)
	}
	return nil
}
