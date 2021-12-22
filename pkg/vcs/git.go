package vcs

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/src-d/go-git.v4"
	"gopkg.in/src-d/go-git.v4/config"
	"gopkg.in/src-d/go-git.v4/plumbing"
	"gopkg.in/src-d/go-git.v4/plumbing/object"
	"gopkg.in/src-d/go-git.v4/plumbing/transport"
	"gopkg.in/src-d/go-git.v4/plumbing/transport/http"
	"gopkg.in/src-d/go-git.v4/plumbing/transport/ssh"
	"gopkg.in/src-d/go-git.v4/storage/memory"
)

const remoteName = "origin"

type gitVCS struct {
	log    logger
	dir    string
	module string
	prefix string
	auth   Auth
}

// NewGit return a go-git VCS client implementation that provides information
// about the specific module using the pgiven authentication mechanism.
func NewGit(l logger, dir string, module string, auth Auth) VCS {
	return &gitVCS{log: l, dir: dir, module: module, auth: auth}
}

func (g *gitVCS) List(ctx context.Context) ([]Version, error) {
	g.log("gitVCS.List", "module", g.module)
	repo, err := g.repo(ctx)
	if err != nil {
		return nil, err
	}

	remote, err := repo.Remote(remoteName)
	if err != nil {
		return nil, err
	}

	auth, err := g.authMethod()
	if err != nil {
		return nil, err
	}

	refs, err := remote.List(&git.ListOptions{Auth: auth})
	if err != nil {
		return nil, err
	}

	list := []Version{}
	var masterHash plumbing.Hash
	tagPrefix := ""
	if g.prefix != "" {
		tagPrefix = g.prefix + "/"
	}
	for _, ref := range refs {
		name := ref.Name()
		if name == plumbing.Master {
			masterHash = ref.Hash()
		} else if name.IsTag() && strings.HasPrefix(name.String(), "refs/tags/"+tagPrefix+"v") {
			list = append(list, Version(strings.TrimPrefix(name.String(), "refs/tags/"+tagPrefix)))
		}
	}

	if len(list) == 0 {
		if masterHash.IsZero() {
			return nil, errors.New("no tags and no master branch found")
		}

		masterCommit, err := repo.CommitObject(masterHash)
		if err != nil {
			return nil, err
		}

		tree, err := masterCommit.Tree()
		if err != nil {
			return nil, err
		}

		if g.isModule(tree) {
			return nil, errors.New("no matching versions")
		}

		hashStr := masterHash.String()
		short := hashStr[:12]
		t, err := g.Timestamp(ctx, Version("v0.0.0-20060102150405-"+short))
		if err != nil {
			return nil, err
		}
		list = []Version{Version(fmt.Sprintf("v0.0.0-%s-%s", t.Format("20060102150405"), short))}
	}

	g.log("gitVCS.List", "module", g.module, "list", list)
	return list, nil
}

func (g *gitVCS) isModule(tree *object.Tree) bool {
	mod := "go.mod"
	for path := g.prefix; path != "."; path = filepath.Dir(path) {
		_, err := tree.FindEntry(filepath.Join(path, mod))
		if err == nil {
			return true
		}
	}
	return false
}

func (g *gitVCS) Timestamp(ctx context.Context, version Version) (time.Time, error) {
	g.log("gitVCS.Timestamp", "module", g.module, "version", version)
	ci, err := g.commit(ctx, version)
	if err != nil {
		return time.Time{}, err
	}
	g.log("gitVCS.Timestamp", "module", g.module, "version", version, "timestamp", ci.Committer.When)
	return ci.Committer.When, nil
}

func isVendoredPackage(name string) bool {
	var i int
	if strings.HasPrefix(name, "vendor/") {
		i += len("vendor/")
	} else if j := strings.Index(name, "/vendor/"); j >= 0 {
		i += len("/vendor/")
	} else {
		return false
	}
	return strings.Contains(name[i:], "/")
}

func (g *gitVCS) Zip(ctx context.Context, version Version) (io.ReadCloser, error) {
	dirName := g.module + "@" + string(version)
	return g.zipUnder(ctx, version, dirName)
}

func (g *gitVCS) zipUnder(ctx context.Context, version Version, dirName string) (io.ReadCloser, error) {
	g.log("gitVCS.Zip", "module", g.module, "version", version)
	ci, err := g.commit(ctx, version)
	if err != nil {
		return nil, err
	}
	tree, err := ci.Tree()
	if err != nil {
		return nil, err
	}

	b := &bytes.Buffer{}
	zw := zip.NewWriter(b)
	modules := map[string]bool{}
	files := []*object.File{}
	tree.Files().ForEach(func(f *object.File) error {
		dir, file := path.Split(f.Name)
		if file == "go.mod" {
			modules[dir] = true
		}
		files = append(files, f)
		return nil
	})
	prefix := g.prefix
	if prefix != "" {
		prefix = prefix + "/"
	}
	submodule := func(name string) bool {
		for {
			dir, _ := path.Split(name)
			if len(dir) <= len(prefix) {
				return false
			}
			if modules[dir] {
				return true
			}
			name = dir[:len(dir)-1]
		}
	}
	for _, f := range files {
		// go mod strips vendored directories from the zip, and we do the same
		// to match the checksums in the go.sum
		if isVendoredPackage(f.Name) {
			continue
		}
		if submodule(f.Name) {
			continue
		}
		mode, err := f.Mode.ToOSFileMode()
		if err != nil {
			return nil, err
		}
		if !mode.IsRegular() {
			continue
		}
		name := f.Name
		if strings.HasPrefix(name, prefix) {
			name = strings.TrimPrefix(name, prefix)
		} else {
			continue
		}
		w, err := zw.Create(filepath.Join(dirName, name))
		if err != nil {
			return nil, err
		}
		r, err := f.Reader()
		if err != nil {
			return nil, err
		}
		defer r.Close()
		io.Copy(w, r)
	}
	zw.Close()
	return ioutil.NopCloser(bytes.NewBuffer(b.Bytes())), nil
}

func (g *gitVCS) repo(ctx context.Context) (repo *git.Repository, err error) {
	repoRoot, path, err := RepoRoot(ctx, g.module)
	if err != nil {
		return nil, err
	}
	g.prefix = path
	if g.dir != "" {
		dir := filepath.Join(g.dir, repoRoot)
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			os.MkdirAll(dir, 0755)
			repo, err = git.PlainInit(dir, true)
		} else {
			return git.PlainOpen(dir)
		}
	} else {
		repo, err = git.Init(memory.NewStorage(), nil)
	}
	if err != nil {
		return nil, err
	}
	schema := "https://"
	if g.auth.Key != "" {
		schema = "ssh://"
	}
	g.log("repo", "url", schema+repoRoot+".git", "prefix", g.prefix)
	_, err = repo.CreateRemote(&config.RemoteConfig{
		Name: remoteName,
		URLs: []string{schema + repoRoot + ".git"},
	})
	return repo, err
}

func (g *gitVCS) commit(ctx context.Context, version Version) (*object.Commit, error) {
	repo, err := g.repo(ctx)
	if err != nil {
		return nil, err
	}
	auth, err := g.authMethod()
	if err != nil {
		return nil, err
	}
	err = repo.FetchContext(ctx, &git.FetchOptions{
		RemoteName: remoteName,
		Auth:       auth,
		Tags:       git.AllTags,
	})
	if err != nil && err != git.NoErrAlreadyUpToDate {
		return nil, err
	}
	tagPrefix := ""
	if g.prefix != "" {
		tagPrefix = g.prefix + "/"
	}

	version = Version(strings.TrimSuffix(string(version), "+incompatible"))
	hash := version.Hash()
	if version.IsSemVer() {
		tags, err := repo.Tags()
		if err != nil {
			return nil, err
		}
		tags.ForEach(func(t *plumbing.Reference) error {
			if t.Name().String() == path.Join("refs/tags", tagPrefix, string(version)) {
				hash = t.Hash().String()
				annotated, err := repo.TagObject(t.Hash())
				if err == nil {
					hash = annotated.Target.String()
				}
			}
			return nil
		})
	} else {
		commits, err := repo.CommitObjects()
		if err != nil {
			return nil, err
		}
		commits.ForEach(func(ci *object.Commit) error {
			if strings.HasPrefix(ci.Hash.String(), version.Hash()) {
				hash = ci.Hash.String()
			}
			return nil
		})
	}

	g.log("gitVCS.commit", "module", g.module, "version", version, "hash", hash)
	return repo.CommitObject(plumbing.NewHash(hash))
}

func (g *gitVCS) authMethod() (transport.AuthMethod, error) {
	if g.auth.Key != "" {
		return ssh.NewPublicKeysFromFile("git", g.auth.Key, "")
	} else if g.auth.Username != "" {
		return &http.BasicAuth{Username: g.auth.Username, Password: g.auth.Password}, nil
	}
	return nil, nil
}
