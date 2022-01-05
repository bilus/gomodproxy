package store

import (
	"context"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/bilus/gomodproxy/pkg/vcs"
)

type disk string

// Disk returns a local disk cache that stores files within a given directory.
func Disk(dir string) Store { return disk(dir) }

func (d disk) Put(ctx context.Context, snapshot Snapshot) error {
	dir := string(d)
	timeFile := filepath.Join(dir, snapshot.Key()+".time")
	zipFile := filepath.Join(dir, snapshot.Key()+".zip")

	if err := os.MkdirAll(filepath.Dir(timeFile), 0755); err != nil {
		return err
	}

	t, err := snapshot.Timestamp.MarshalText()
	if err != nil {
		return err
	}
	if err := ioutil.WriteFile(timeFile, t, 0644); err != nil {
		return err
	}
	return ioutil.WriteFile(zipFile, snapshot.Data, 0644)
}

func (d disk) Get(ctx context.Context, module string, version vcs.Version) (Snapshot, error) {
	dir := string(d)
	s := Snapshot{Module: module, Version: version}
	t, err := ioutil.ReadFile(filepath.Join(dir, s.Key()+".time"))
	if err != nil {
		return Snapshot{}, err
	}
	if err := s.Timestamp.UnmarshalText(t); err != nil {
		return Snapshot{}, err
	}
	s.Data, err = ioutil.ReadFile(filepath.Join(dir, s.Key()+".zip"))
	return s, err
}

func (d disk) Del(ctx context.Context, module string, version vcs.Version) error {
	dir := string(d)
	s := Snapshot{Module: module, Version: version}
	err := os.Remove(filepath.Join(dir, s.Key()+".time"))
	if err != nil {
		return err
	}
	err = os.Remove(filepath.Join(dir, s.Key()+".zip"))
	return err
}

func (d disk) Close() error { return nil }
