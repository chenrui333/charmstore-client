// Copyright 2016 Canonical Ltd.
// Licensed under the GPLv3, see LICENCE file for details.

package charmcmd

import (
	"fmt"
	"io"
	"sort"

	"github.com/gosuri/uitable"
	"github.com/juju/charmrepo/v6/csclient/params"
	"github.com/juju/cmd"
	"github.com/juju/gnuflag"
	"gopkg.in/errgo.v1"
)

type termsCommand struct {
	cmd.CommandBase
	auth authInfo

	out  cmd.Output
	user string
}

var termsDoc = `
lists the terms required by the current user's charms
   charm terms-used
`

// Info implements cmd.Command.Info.
func (c *termsCommand) Info() *cmd.Info {
	return &cmd.Info{
		Name:    "terms-used",
		Purpose: "list terms required by current user's charms",
		Doc:     termsDoc,
	}
}

// SetFlags implements cmd.Command.SetFlags.
func (c *termsCommand) SetFlags(f *gnuflag.FlagSet) {
	c.out.AddFlags(f, "tabular", map[string]cmd.Formatter{
		"yaml":    cmd.FormatYaml,
		"json":    cmd.FormatJson,
		"tabular": formatTermsTabular,
	})
	f.StringVar(&c.user, "u", "", "the given user name")
	addAuthFlags(f, &c.auth)
}

// Init implements cmd.Command.Init.
func (c *termsCommand) Init(args []string) error {
	return cmd.CheckEmpty(args)
}

type termsResponse struct {
	Terms []string `json:"terms"`
}

// Run implements cmd.Command.Run.
func (c *termsCommand) Run(ctxt *cmd.Context) error {
	client, err := newCharmStoreClient(ctxt, c.auth, params.NoChannel)
	if err != nil {
		return errgo.Notef(err, "cannot create charm store client")
	}
	defer client.jar.Save()

	if c.user == "" {
		resp, err := client.WhoAmI()
		if err != nil {
			return errgo.Notef(err, "cannot retrieve identity")
		}
		c.user = resp.User
	}

	if err := validateNames([]string{c.user}); err != nil {
		return errgo.Mask(err)
	}

	// We sort here so that our output to the user will be consistent.
	// TODO (mattyw) This only lists the latest version of each charm
	// which might not be what we want in the future.
	path := "/list?owner=" + c.user + "&sort=name,-series"
	var resp params.ListResponse
	if err := client.Get(path, &resp); err != nil {
		return errgo.Notef(err, "cannot list charms for user %s", path)
	}
	output := make(map[string][]string)
	for _, charm := range resp.Results {
		var resp termsResponse
		// TODO (mattyw) We could make a bulk meta request in future.
		if _, err := client.Meta(charm.Id, &resp); err != nil {
			return errgo.Notef(err, "cannot list terms for charm %s", charm.Id.String())
		}
		for _, term := range resp.Terms {
			output[term] = append(output[term], charm.Id.String())
		}
	}
	return c.out.Write(ctxt, output)
}

// formatTermsTabular returns a tabular summary of terms owned by the user.
func formatTermsTabular(w io.Writer, terms0 interface{}) error {
	terms, ok := terms0.(map[string][]string)
	if !ok {
		return errgo.Newf("expected value of type %T", terms0)
	}
	if len(terms) == 0 {
		fmt.Fprint(w, "No terms found.")
		return nil
	}

	sortedTerms := make([]string, 0, len(terms))
	for term := range terms {
		sortedTerms = append(sortedTerms, term)
	}
	sort.Strings(sortedTerms)

	table := uitable.New()
	table.MaxColWidth = 50
	table.Wrap = true

	table.AddRow("TERM", "CHARM")
	for _, term := range sortedTerms {
		charms := terms[term]
		for i, charm := range charms {
			if i == 0 {
				table.AddRow(term, charm)
			} else {
				table.AddRow("", charm)
			}
		}
	}

	fmt.Fprint(w, table.String())
	return nil
}
