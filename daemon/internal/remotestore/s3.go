package remotestore

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"elb/daemon/internal/settings"
)

type s3Store struct {
	client *minio.Client
	bucket string
}

func s3Endpoint(raw string) (host string, secure bool) {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host, u.Scheme != "http"
	}
	return strings.TrimSuffix(raw, "/"), true
}

func newS3Store(d settings.BackupDestination) (*s3Store, error) {
	host, secure := s3Endpoint(d.S3Endpoint)
	client, err := minio.New(host, &minio.Options{
		Creds:  credentials.NewStaticV4(d.S3AccessKey, d.S3SecretKey, ""),
		Secure: secure,
		Region: d.S3Region,
	})
	if err != nil {
		return nil, fmt.Errorf("s3 client %s: %w", host, err)
	}
	return &s3Store{client: client, bucket: d.S3Bucket}, nil
}

func (s *s3Store) List() ([]Entry, error) {
	ctx := context.Background()
	var out []Entry
	for obj := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{}) {
		if obj.Err != nil {
			return nil, fmt.Errorf("list objects: %w", obj.Err)
		}
		out = append(out, Entry{Name: obj.Key, SizeBytes: obj.Size})
	}
	return out, nil
}

func (s *s3Store) Put(localPath, remoteName string, throttleBytesPerSec int64) error {
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	ctx := context.Background()
	if _, err := s.client.PutObject(ctx, s.bucket, remoteName, newThrottledReader(f, throttleBytesPerSec), info.Size(), minio.PutObjectOptions{}); err != nil {
		return fmt.Errorf("upload %s: %w", remoteName, err)
	}
	return nil
}

func (s *s3Store) Open(remoteName string) (io.ReadCloser, error) {
	ctx := context.Background()
	obj, err := s.client.GetObject(ctx, s.bucket, remoteName, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("чтение %s: %w", remoteName, err)
	}

	if _, err := obj.Stat(); err != nil {
		obj.Close()
		return nil, fmt.Errorf("чтение %s: %w", remoteName, err)
	}
	return obj, nil
}

func (s *s3Store) Get(remoteName, localPath string) error {
	ctx := context.Background()
	if err := s.client.FGetObject(ctx, s.bucket, remoteName, localPath, minio.GetObjectOptions{}); err != nil {
		return fmt.Errorf("скачивание %s: %w", remoteName, err)
	}
	return nil
}

func (s *s3Store) Delete(remoteName string) error {
	ctx := context.Background()
	if err := s.client.RemoveObject(ctx, s.bucket, remoteName, minio.RemoveObjectOptions{}); err != nil {
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return nil
		}
		return fmt.Errorf("delete %s: %w", remoteName, err)
	}
	return nil
}

func (s *s3Store) Test() error {
	ctx := context.Background()
	ok, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("проверка бакета %s: %w", s.bucket, err)
	}
	if !ok {
		return fmt.Errorf("бакет %q не найден", s.bucket)
	}

	if _, err := s.client.PutObject(ctx, s.bucket, testObjectName, bytes.NewReader(nil), 0, minio.PutObjectOptions{}); err != nil {
		return fmt.Errorf("тестовая запись %s: %w", testObjectName, err)
	}
	if err := s.Delete(testObjectName); err != nil {
		return fmt.Errorf("удаление тестового файла %s: %w", testObjectName, err)
	}
	return nil
}
