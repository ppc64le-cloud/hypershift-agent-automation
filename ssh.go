package main

import (
	"bytes"
	"context"
	"fmt"
	scp "github.com/bramvdbogaerde/go-scp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
	"os"
)

func createSSHClient(host, username, password string) (*ssh.Client, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	hostKeyCallback, err := knownhosts.New(fmt.Sprintf("%s/.ssh/known_hosts", homeDir))
	if err != nil {
		return nil, err
	}

	config := &ssh.ClientConfig{
		User: username,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
		},
		HostKeyCallback: hostKeyCallback,
	}
	conn, err := ssh.Dial("tcp", fmt.Sprintf("%s:22", host), config)
	if err != nil {
		return nil, fmt.Errorf("error establishing ssh connection to %s %v", host, err)
	}

	return conn, nil
}

func executeCommand(client *ssh.Client, command string) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("error creating ssh session %v", err)
	}
	var buff bytes.Buffer
	session.Stdout = &buff
	if err = session.Run(command); err != nil {
		return "", fmt.Errorf("error running command %s, %v", command, err)
	}

	return buff.String(), nil
}

func scpFile(sshClient *ssh.Client, srcFile, destDir string) error {
	client, err := scp.NewClientBySSH(sshClient)
	if err != nil {
		fmt.Println("error creating new ssh session from existing connection", err)
	}
	f, err := os.Open(srcFile)
	if err != nil {
		return fmt.Errorf("error on open source file: %v", err)
	}
	defer client.Close()
	logger.Infof("scp iso %s to vios started", srcFile)
	return client.CopyFromFile(context.Background(), *f, fmt.Sprintf("%s/%s", destDir, srcFile), "0644")
}
