package vcs

import (
	"context"
	"io"
	"regexp"
	"strings"
	"time"
)

type logger = func(v ...interface{})

// Version represents a semantic version of a module.
type Version string

var reSemVer = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

// IsSemVer returns true if a version string is a semantic version e.g. vX.Y.Z.
func (v Version) IsSemVer() bool { return reSemVer.MatchString(string(v)) }

// Hash returns a commit hash if a version is of a form v0.0.0-timestamp-hash.
func (v Version) Hash() string {
	fields := strings.Split(string(v), "-")
	if len(fields) != 3 {
		return ""
	}
	return fields[2]
}

// String returns a string representation of a version
func (v Version) String() string {
	return string(v)
}

// Module is a source code snapshot for which one can get the commit timestamp
// or the actual ZIP with the source code in it.
type Module interface {
	Timestamp(ctx context.Context, version Version) (time.Time, error)
	Zip(ctx context.Context, version Version) (io.ReadCloser, error)
}

// VCS is a version control system client. It can list available versions from
// the remote, as well as fetch module data such as timestamp or zip snapshot.
type VCS interface {
	List(ctx context.Context) ([]Version, error)
	Module
}

// Auth defines a typical VCS authentication mechanism, such as SSH key or
// username/password.
type Auth struct {
	Username string
	Password string
	Key      string
}

// NoAuth returns an Auth implementation that uses no authentication at all.
func NoAuth() Auth { return Auth{} }

// Password returns an Auth implementation that authenticate via username and password.
func Password(username, password string) Auth { return Auth{Username: username, Password: password} }

// Key returns an Auth implementation that uses key file authentication mechanism.
func Key(key, password string) Auth { return Auth{Key: key, Password: password} }
