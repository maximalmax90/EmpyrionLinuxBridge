package remotestore

import (
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"strconv"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"elb/daemon/internal/settings"
)

type sftpStore struct {
	host, user, password, key, dir string
	port                           int
}

func newSFTPStore(d settings.BackupDestination) *sftpStore {
	port := d.FTPPort
	if port == 0 {
		port = 22
	}
	return &sftpStore{host: d.FTPHost, port: port, user: d.FTPUser, password: d.FTPPassword, key: d.FTPKey, dir: d.FTPPath}
}

func (s *sftpStore) connect() (*sftp.Client, *ssh.Client, error) {
	var auth []ssh.AuthMethod
	if s.key != "" {
		signer, err := ssh.ParsePrivateKey([]byte(s.key))
		if err != nil {
			return nil, nil, fmt.Errorf("parsing private key: %w", err)
		}
		auth = append(auth, ssh.PublicKeys(signer))
	} else {
		auth = append(auth, ssh.Password(s.password))
	}
	cfg := &ssh.ClientConfig{
		User:            s.user,
		Auth:            auth,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         15 * time.Second,
	}
	addr := net.JoinHostPort(s.host, strconv.Itoa(s.port))
	sshc, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("connecting to %s: %w", addr, err)
	}
	sc, err := sftp.NewClient(sshc)
	if err != nil {
		sshc.Close()
		return nil, nil, fmt.Errorf("sftp session: %w", err)
	}
	return sc, sshc, nil
}

func (s *sftpStore) remotePath(name string) string {
	if s.dir == "" {
		return name
	}
	return path.Join(s.dir, name)
}

func (s *sftpStore) dirOrRoot() string {
	if s.dir == "" {
		return "."
	}
	return s.dir
}

func (s *sftpStore) List() ([]Entry, error) {
	sc, sshc, err := s.connect()
	if err != nil {
		return nil, err
	}
	defer sc.Close()
	defer sshc.Close()
	dir := s.dirOrRoot()
	infos, err := sc.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("list files %s: %w", dir, err)
	}
	var out []Entry
	for _, fi := range infos {
		if fi.IsDir() {
			continue
		}
		out = append(out, Entry{Name: fi.Name(), SizeBytes: fi.Size()})
	}
	return out, nil
}

func (s *sftpStore) Put(localPath, remoteName string, throttleBytesPerSec int64) error {
	sc, sshc, err := s.connect()
	if err != nil {
		return err
	}
	defer sc.Close()
	defer sshc.Close()
	if s.dir != "" {
		if err := sc.MkdirAll(s.dir); err != nil {
			return fmt.Errorf("creating directory %s: %w", s.dir, err)
		}
	}
	src, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := sc.Create(s.remotePath(remoteName))
	if err != nil {
		return fmt.Errorf("creating %s: %w", remoteName, err)
	}
	defer dst.Close()
	if _, err := io.Copy(dst, newThrottledReader(src, throttleBytesPerSec)); err != nil {
		return fmt.Errorf("upload %s: %w", remoteName, err)
	}
	return nil
}

type sftpReadCloser struct {
	io.ReadCloser
	sc   *sftp.Client
	sshc *ssh.Client
}

func (r *sftpReadCloser) Close() error {
	err := r.ReadCloser.Close()
	r.sc.Close()
	r.sshc.Close()
	return err
}

func (s *sftpStore) Open(remoteName string) (io.ReadCloser, error) {
	sc, sshc, err := s.connect()
	if err != nil {
		return nil, err
	}
	f, err := sc.Open(s.remotePath(remoteName))
	if err != nil {
		sc.Close()
		sshc.Close()
		return nil, fmt.Errorf("чтение %s: %w", remoteName, err)
	}
	return &sftpReadCloser{ReadCloser: f, sc: sc, sshc: sshc}, nil
}

func (s *sftpStore) Get(remoteName, localPath string) error {
	rc, err := s.Open(remoteName)
	if err != nil {
		return err
	}
	defer rc.Close()
	dst, err := os.Create(localPath)
	if err != nil {
		return err
	}
	defer dst.Close()
	if _, err := io.Copy(dst, rc); err != nil {
		return fmt.Errorf("скачивание %s: %w", remoteName, err)
	}
	return nil
}

func (s *sftpStore) Delete(remoteName string) error {
	sc, sshc, err := s.connect()
	if err != nil {
		return err
	}
	defer sc.Close()
	defer sshc.Close()
	if err := sc.Remove(s.remotePath(remoteName)); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("delete %s: %w", remoteName, err)
	}
	return nil
}

func (s *sftpStore) Test() error {
	sc, sshc, err := s.connect()
	if err != nil {
		return err
	}
	defer sc.Close()
	defer sshc.Close()
	dir := s.dirOrRoot()
	if _, err := sc.ReadDir(dir); err != nil {
		return fmt.Errorf("список файлов %s: %w", dir, err)
	}

	remotePath := s.remotePath(testObjectName)
	f, err := sc.Create(remotePath)
	if err != nil {
		return fmt.Errorf("тестовая запись %s: %w", testObjectName, err)
	}
	f.Close()
	if err := sc.Remove(remotePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("удаление тестового файла %s: %w", testObjectName, err)
	}
	return nil
}
