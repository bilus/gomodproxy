package vcs

import (
	"context"
	"fmt"
	"io"
	"time"
)

type ephemeralTag struct {
	semVer Version
	short  string
}

type EphemeralTagStorage struct {
	tagsByModule map[moduleName][]ephemeralTag
}

func NewEphemeralTagStorage() *EphemeralTagStorage {
	return &EphemeralTagStorage{
		tagsByModule: make(map[moduleName][]ephemeralTag),
	}
}

func (s *EphemeralTagStorage) Tag(module string, semVer Version, short string) error {
	tags := s.tagsByModule[module]
	tmp := tags[:0]
	for _, t := range tags {
		if t.semVer != semVer {
			tmp = append(tmp, t)
		}
	}
	s.tagsByModule[module] = append(tmp, ephemeralTag{semVer, short})
	return nil
}

func (s *EphemeralTagStorage) tags(module string) []ephemeralTag {
	return s.tagsByModule[module]
}

type moduleName = string

type taggableVCS struct {
	wrapped *gitVCS
	module  string
	storage *EphemeralTagStorage
}

type Taggable interface {
	Tag(ctx context.Context, semVer Version, short string) error
}

// NewGitWithEphemeralTags return a go-git VCS client implementation that
// provides information about the specific module using the given
// authentication mechanism while adding support to ephemeral tags.
func NewGitWithEphemeralTags(l logger, dir string, module string, auth Auth, storage *EphemeralTagStorage) VCS {
	git := NewGit(l, dir, module, auth).(*gitVCS)
	return &taggableVCS{
		wrapped: git,
		module:  module,
		storage: storage,
	}
}

func (v *taggableVCS) safeList(ctx context.Context) ([]Version, error) {
	remoteVersions, err := v.wrapped.List(ctx)
	if err != nil {
		// Ignore this error, we can still count on ephemeral tags.
		if err != ErrNoMatchingVersion {
			return nil, err
		}
		v.wrapped.log("No remote version tags yet:", err)
	}
	return remoteVersions, nil
}

func (v *taggableVCS) Tag(ctx context.Context, semVer Version, short string) error {
	remoteVersions, err := v.safeList(ctx)
	if err != nil {
		return err
	}
	if versionExists(remoteVersions, semVer) {
		return fmt.Errorf("remote version %s already exists for module %s", semVer, v.module)
	}
	return v.storage.Tag(v.module, semVer, short)
}

func (v *taggableVCS) List(ctx context.Context) ([]Version, error) {
	remoteVersions, err := v.safeList(ctx)
	if err != nil {
		return nil, err
	}
	tags := v.storage.tags(v.module)
	// Remote versions win.
	return appendEphemeralVersion(remoteVersions, tags...), nil
}

func appendEphemeralVersion(versions []Version, tags ...ephemeralTag) []Version {
	ephemeral := make([]Version, 0)
	for _, tag := range tags {
		if !versionExists(versions, tag.semVer) {
			ephemeral = append(ephemeral, tag.semVer)
		}
	}
	return append(versions, ephemeral...)
}

func versionExists(versions []Version, v Version) bool {
	for _, v2 := range versions {
		if v == v2 {
			return true
		}
	}
	return false
}

func (v *taggableVCS) Timestamp(ctx context.Context, version Version) (time.Time, error) {
	version2, err := v.resolveVersion(ctx, version)
	if err != nil {
		return time.Time{}, err
	}
	return v.wrapped.Timestamp(ctx, version2)
}

func (v *taggableVCS) Zip(ctx context.Context, version Version) (io.ReadCloser, error) {
	version2, err := v.resolveVersion(ctx, version)
	if err != nil {
		return nil, err
	}
	// Zip must contain the ephemeral version.
	dirName := v.module + "@" + string(version)
	return v.wrapped.zipAs(ctx, version2, dirName)
}

func (v *taggableVCS) resolveVersion(ctx context.Context, version Version) (Version, error) {
	for _, tag := range v.storage.tags(v.module) {
		if tag.semVer == version {
			return v.wrapped.versionFromHash(ctx, tag.short)
		}
	}
	return version, nil
}
