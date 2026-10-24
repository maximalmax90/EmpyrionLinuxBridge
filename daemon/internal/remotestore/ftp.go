package remotestore

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/textproto"
	"os"
	"time"

	"github.com/jlaffaye/ftp"

	"elb/daemon/internal/settings"
)

type ftpStore struct {
	host, user, password, dir string
	port                      int
}

func newFTPStore(d settings.BackupDestination) *ftpStore {
	port := d.FTPPort
	if port == 0 {
		port = 21
	}
	return &ftpStore{host: d.FTPHost, port: port, user: d.FTPUser, password: d.FTPPassword, dir: d.FTPPath}
}

func (s *ftpStore) connect() (*ftp.ServerConn, error) {
	addr := fmt.Sprintf("%s:%d", s.host, s.port)
	c, err := ftp.Dial(addr, ftp.DialWithTimeout(15*time.Second))
	if err != nil {
		return nil, fmt.Errorf("connecting to %s: %w", addr, err)
	}
	if err := c.Login(s.user, s.password); err != nil {
		c.Quit()
		return nil, fmt.Errorf("login %s: %w", s.user, err)
	}
	if s.dir != "" {
		if err := c.MakeDir(s.dir); err != nil {

			_ = err
		}
		if err := c.ChangeDir(s.dir); err != nil {
			c.Quit()
			return nil, fmt.Errorf("changing to %s: %w", s.dir, err)
		}
	}
	return c, nil
}

func ftpNotExist(err error) bool {
	var pe *textproto.Error
	if errors.As(err, &pe) {
		return pe.Code == ftp.StatusFileUnavailable
	}
	return false
}

func (s *ftpStore) List() ([]Entry, error) {
	c, err := s.connect()
	if err != nil {
		return nil, err
	}
	defer c.Quit()
	entries, err := c.List("")
	if err != nil {
		return nil, fmt.Errorf("list files: %w", err)
	}
	var out []Entry
	for _, e := range entries {
		if e.Type != ftp.EntryTypeFile {
			continue
		}
		out = append(out, Entry{Name: e.Name, SizeBytes: int64(e.Size)})
	}
	return out, nil
}

func (s *ftpStore) Put(localPath, remoteName string, throttleBytesPerSec int64) error {
	c, err := s.connect()
	if err != nil {
		return err
	}
	defer c.Quit()
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := c.Stor(remoteName, newThrottledReader(f, throttleBytesPerSec)); err != nil {
		return fmt.Errorf("upload %s: %w", remoteName, err)
	}
	return nil
}

type ftpReadCloser struct {
	*ftp.Response
	conn *ftp.ServerConn
}

func (r *ftpReadCloser) Close() error {
	err := r.Response.Close()
	r.conn.Quit()
	return err
}

func (s *ftpStore) Open(remoteName string) (io.ReadCloser, error) {
	c, err := s.connect()
	if err != nil {
		return nil, err
	}
	r, err := c.Retr(remoteName)
	if err != nil {
		c.Quit()
		return nil, fmt.Errorf("чтение %s: %w", remoteName, err)
	}
	return &ftpReadCloser{Response: r, conn: c}, nil
}

func (s *ftpStore) Get(remoteName, localPath string) error {
	rc, err := s.Open(remoteName)
	if err != nil {
		return err
	}
	defer rc.Close()
	f, err := os.Create(localPath)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, rc); err != nil {
		return fmt.Errorf("скачивание %s: %w", remoteName, err)
	}
	return nil
}

func (s *ftpStore) Delete(remoteName string) error {
	c, err := s.connect()
	if err != nil {
		return err
	}
	defer c.Quit()
	if err := c.Delete(remoteName); err != nil {
		if ftpNotExist(err) {
			return nil
		}
		return fmt.Errorf("delete %s: %w", remoteName, err)
	}
	return nil
}

func (s *ftpStore) Test() error {
	c, err := s.connect()
	if err != nil {
		return err
	}
	defer c.Quit()
	if _, err := c.List(""); err != nil {
		return fmt.Errorf("список файлов: %w", err)
	}

	if err := c.Stor(testObjectName, bytes.NewReader(nil)); err != nil {
		return fmt.Errorf("тестовая запись %s: %w", testObjectName, err)
	}
	if err := c.Delete(testObjectName); err != nil && !ftpNotExist(err) {
		return fmt.Errorf("удаление тестового файла %s: %w", testObjectName, err)
	}
	return nil
}
