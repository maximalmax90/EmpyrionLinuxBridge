package remotestore

import (
	"fmt"
	"io"

	"elb/daemon/internal/settings"
)

type Entry struct {
	Name      string
	SizeBytes int64
}

type Store interface {
	List() ([]Entry, error)

	Put(localPath, remoteName string, throttleBytesPerSec int64) error

	Open(remoteName string) (io.ReadCloser, error)

	Get(remoteName, localPath string) error

	Delete(remoteName string) error

	Test() error
}

const testObjectName = ".elb-write-test"

func New(d settings.BackupDestination) (Store, error) {
	switch d.Type {
	case "ftp":
		if d.FTPProtocol == "sftp" {
			return newSFTPStore(d), nil
		}
		return newFTPStore(d), nil
	case "s3":
		return newS3Store(d)
	default:
		return nil, fmt.Errorf("remotestore: unsupported storage type %q", d.Type)
	}
}
