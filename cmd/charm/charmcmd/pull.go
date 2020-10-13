// Copyright 2014 Canonical Ltd.
// Licensed under the GPLv3, see LICENCE file for details.

package charmcmd

import (
	"crypto/sha512"
	"fmt"
	"io"
	"io/ioutil"
	"os"

	"github.com/juju/charmrepo/v6/csclient"
	"github.com/juju/charmrepo/v6/csclient/params"
	"github.com/juju/cmd"
	"github.com/juju/gnuflag"
	"gopkg.in/errgo.v1"

	"github.com/juju/charmstore-client/internal/charm"
)

type pullCommand struct {
	cmd.CommandBase

	id      *charm.URL
	destDir string
	channel chanValue

	auth authInfo
}

// These values are exposed as variables so that
// they can be changed for testing purposes.
var clientGetArchive = (*csclient.Client).GetArchive

var pullDoc = `
The pull command downloads a copy of a charm or bundle
from the charm store into a local directory.
If the directory is unspecified, the directory
will be named after the charm or bundle, so:

   charm pull trusty/wordpress

will fetch the wordpress charm into the
directory "wordpress" in the current directory.

To select a channel, use the --channel option, for instance:

	charm pull wordpress --channel edge
`

func (c *pullCommand) Info() *cmd.Info {
	return &cmd.Info{
		Name:    "pull",
		Args:    "<charm or bundle id> [--channel <channel>] [<directory>]",
		Purpose: "download a charm or bundle from the charm store",
		Doc:     pullDoc,
	}
}

func (c *pullCommand) SetFlags(f *gnuflag.FlagSet) {
	addChannelFlag(f, &c.channel, nil)
	addAuthFlags(f, &c.auth)
}

func (c *pullCommand) Init(args []string) error {
	if len(args) == 0 {
		return errgo.New("no charm or bundle id specified")
	}
	if len(args) > 2 {
		return errgo.New("too many arguments")
	}

	id, err := charm.ParseURL(args[0])
	if err != nil {
		return errgo.Notef(err, "invalid charm or bundle id %q", args[0])
	}
	c.id = id
	if len(args) > 1 {
		c.destDir = args[1]
	} else {
		c.destDir = id.Name
	}
	return nil
}

func (c *pullCommand) Run(ctxt *cmd.Context) error {
	destDir := ctxt.AbsPath(c.destDir)
	if _, err := os.Stat(destDir); err == nil || !os.IsNotExist(err) {
		return errgo.Newf("directory %q already exists", destDir)
	}
	channel := params.NoChannel
	if c.id.Revision == -1 {
		channel = c.channel.C
	}
	client, err := newCharmStoreClient(ctxt, c.auth, channel)
	if err != nil {
		return errgo.Notef(err, "cannot create charm store client")
	}
	defer client.jar.Save()

	r, id, expectHash, _, err := clientGetArchive(client.Client, c.id)
	if err != nil {
		return err
	}
	defer r.Close()

	f, err := ioutil.TempFile("", "charm")
	if err != nil {
		return errgo.Notef(err, "cannot make temporary file")
	}
	defer f.Close()
	defer os.Remove(f.Name())
	hash := sha512.New384()
	_, err = io.Copy(io.MultiWriter(hash, f), r)
	if err != nil {
		return errgo.Notef(err, "cannot read archive")
	}
	gotHash := fmt.Sprintf("%x", hash.Sum(nil))
	if gotHash != expectHash {
		return errgo.Newf("hash mismatch; network corruption?")
	}
	var entity interface {
		ExpandTo(dir string) error
	}
	if id.Series == "bundle" {
		entity, err = charm.ReadBundleArchive(f.Name())
	} else {
		entity, err = charm.ReadCharmArchive(f.Name())
	}
	if err != nil {
		return errgo.Notef(err, "cannot read %s archive", c.id)
	}
	err = entity.ExpandTo(destDir)
	if err != nil {
		return errgo.Notef(err, "cannot expand %s archive", c.id)
	}
	fmt.Fprintln(ctxt.Stdout, id)
	return nil
}
