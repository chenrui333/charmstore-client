// Copyright 2015-2016 Canonical Ltd.
// Licensed under the GPLv3, see LICENCE file for details.

package charmcmd_test

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"text/template"
	"time"

	qt "github.com/frankban/quicktest"
	"github.com/juju/utils"

	"github.com/juju/charmstore-client/cmd/charm/charmcmd"
)

func TestPlugin(t *testing.T) {
	RunSuite(qt.New(t), &pluginSuite{})
}

type pluginSuite struct {
	dir  string
	dir2 string
}

func (s *pluginSuite) Init(c *qt.C) {
	if runtime.GOOS == "windows" {
		c.Skip("tests use bash scripts")
	}
	fakeHome(c)
	charmcmd.WhiteListedCommands["foo"] = true
	charmcmd.WhiteListedCommands["bar"] = true
	charmcmd.WhiteListedCommands["baz"] = true
	charmcmd.WhiteListedCommands["error"] = true
	s.dir = c.Mkdir()
	s.dir2 = c.Mkdir()
	c.Setenv("PATH", s.dir+":"+s.dir2)
	charmcmd.ResetPluginDescriptionsResults()
	os.Remove("/tmp/.cache/charm-command-cache")
	os.Remove(filepath.Join(utils.Home(), ".cache/charm-command-cache"))
}

func (*pluginSuite) TestPluginHelpNoPlugins(c *qt.C) {
	stdout, stderr := runHelp(c)
	c.Assert(stdout, qt.Equals, "No plugins found.\n")
	c.Assert(stderr, qt.Equals, "")
}

func (s *pluginSuite) TestPluginHelpOrder(c *qt.C) {
	s.makePlugin("foo", 0744)
	s.makePlugin("bar", 0744)
	s.makePlugin("baz", 0744)
	stdout, stderr := runHelp(c)
	c.Assert(stdout, qt.Equals, `bar  bar --description
baz  baz --description
foo  foo --description
`)
	c.Assert(stderr, qt.Equals, "")
}

func (s *pluginSuite) TestPluginHelpIgnoreNotExecutable(c *qt.C) {
	s.makePlugin("foo", 0644)
	s.makePlugin("bar", 0666)
	stdout, stderr := runHelp(c)
	c.Assert(stdout, qt.Equals, "No plugins found.\n")
	c.Assert(stderr, qt.Equals, "")
}

func (s *pluginSuite) TestPluginHelpSpecificCommand(c *qt.C) {
	s.makeFullPlugin(pluginParams{Name: "foo"})
	stdout, stderr, code := run(c.Mkdir(), "help", "foo")
	c.Assert(stderr, qt.Equals, "")
	c.Assert(code, qt.Equals, 0)
	c.Assert(stdout, qt.Equals, `
foo longer help

something useful
`[1:])
}

func (s *pluginSuite) TestPluginHelpCommandNotFound(c *qt.C) {
	stdout, stderr, code := run(c.Mkdir(), "help", "foo")
	c.Assert(stderr, qt.Equals, "ERROR unknown command or topic for foo\n")
	c.Assert(code, qt.Equals, 1)
	c.Assert(stdout, qt.Equals, "")
}

func (s *pluginSuite) TestPluginHelpRunInParallel(c *qt.C) {
	// Make plugins that will deadlock if we don't start them in parallel.
	// Each plugin depends on another one being started before they will
	// complete. They make a full loop, so no sequential ordering will ever
	// succeed.
	s.makeFullPlugin(pluginParams{Name: "foo", Creates: "foo", DependsOn: "bar"})
	s.makeFullPlugin(pluginParams{Name: "bar", Creates: "bar", DependsOn: "baz"})
	s.makeFullPlugin(pluginParams{Name: "baz", Creates: "baz", DependsOn: "error"})
	s.makeFullPlugin(pluginParams{Name: "error", ExitStatus: 1, Creates: "error", DependsOn: "foo"})

	// If the code was wrong, getPluginDescriptions would deadlock,
	// so timeout after a short while.
	outputChan := make(chan string)
	go func() {
		stdout, stderr := runHelp(c)
		c.Assert(stderr, qt.Equals, "")
		outputChan <- stdout
	}()
	// This time is arbitrary but should always be generously long. Test
	// actually only takes about 15ms in practice, but 10s allows for system
	// hiccups, etc.
	wait := 5 * time.Second
	var output string
	select {
	case output = <-outputChan:
	case <-time.After(wait):
		c.Fatalf("took longer than %fs to complete", wait.Seconds())
	}
	c.Assert(output, qt.Equals, `bar    bar --description
baz    baz --description
error  error occurred running 'charm-error --description'
foo    foo --description
`)
}

func (s *pluginSuite) TestPluginRun(c *qt.C) {
	s.makePlugin("foo", 0755)
	stdout, stderr, code := run(c.Mkdir(), "foo", "some", "params")
	c.Assert(stderr, qt.Equals, "")
	c.Assert(code, qt.Equals, 0)
	c.Assert(stdout, qt.Equals, "foo some params\n")
}

func (s *pluginSuite) TestPluginRunWithError(c *qt.C) {
	s.makeFailingPlugin("foo", 2)
	stdout, stderr, code := run(c.Mkdir(), "foo", "some", "params")
	c.Assert(stderr, qt.Equals, "")
	c.Assert(code, qt.Equals, 2)
	c.Assert(stdout, qt.Equals, "failing\n")
}

func (s *pluginSuite) TestPluginRunWithHelpFlag(c *qt.C) {
	s.makeFullPlugin(pluginParams{Name: "foo"})
	stdout, stderr, code := run(c.Mkdir(), "foo", "--help")
	c.Assert(stderr, qt.Equals, "")
	c.Assert(code, qt.Equals, 0)
	c.Assert(stdout, qt.Equals, `
foo longer help

something useful
`[1:])
}

func (s *pluginSuite) TestPluginRunWithDebugFlag(c *qt.C) {
	s.makeFullPlugin(pluginParams{Name: "foo"})
	stdout, stderr, code := run(c.Mkdir(), "foo", "--debug")
	c.Check(stderr, qt.Equals, "")
	c.Check(code, qt.Equals, 0)
	c.Check(stdout, qt.Equals, "some debug\n")
}

func (s *pluginSuite) TestPluginRunWithEnvVars(c *qt.C) {
	s.makeFullPlugin(pluginParams{Name: "foo"})
	c.Setenv("ANSWER", "42")
	stdout, stderr, code := run(c.Mkdir(), "foo")
	c.Check(stderr, qt.Equals, "")
	c.Check(code, qt.Equals, 0)
	c.Check(stdout, qt.Equals, "foo\nanswer is 42\n")
}

func (s *pluginSuite) TestPluginRunWithMultipleNamesInPath(c *qt.C) {
	s.makeFullPlugin(pluginParams{Name: "foo"})
	c.Setenv("ANSWER", "42")
	s.makeFullPluginInSecondDir(pluginParams{Name: "foo"})
	stdout, stderr, code := run(c.Mkdir(), "foo")
	c.Assert(stderr, qt.Equals, "")
	c.Assert(code, qt.Equals, 0)
	c.Assert(stdout, qt.Equals, "foo\nanswer is 42\n")
}

func (s *pluginSuite) TestPluginRunWithUnknownFlag(c *qt.C) {
	s.makeFullPlugin(pluginParams{Name: "foo"})
	stdout, stderr, code := run(c.Mkdir(), "foo", "--unknown-to-juju")
	c.Assert(stderr, qt.Matches, "")
	c.Assert(code, qt.Equals, 0)
	c.Assert(stdout, qt.Equals, "the flag was still there.\n")
}

func (s *pluginSuite) TestHelp(c *qt.C) {
	s.makeFullPlugin(pluginParams{Name: "foo"})
	s.makeFullPlugin(pluginParams{Name: "bar"})
	s.makeFullPlugin(pluginParams{Name: "help"}) // Duplicates "help" command.
	s.makeFullPlugin(pluginParams{Name: "list"}) // Duplicates existing "list" command.
	s.makeFullPluginInSecondDir(pluginParams{Name: "foo"})

	stdout, stderr, code := run(c.Mkdir(), "help")
	c.Assert(stderr, qt.Matches, "")
	c.Assert(code, qt.Equals, 0)
	c.Assert(stdout, qt.Matches, `
(.|\n)*    bar                 - bar --description
(.|\n)*    foo                 - foo --description
(.|\n)*    help                - Show help on a command or other topic.
(.|\n)*    list                - list charms for the given users.
(.|\n)*
`[1:])
}

func (s *pluginSuite) TestWhiteListWorks(c *qt.C) {
	s.makeFullPlugin(pluginParams{Name: "foo"})
	s.makeFullPlugin(pluginParams{Name: "danger"})
	stdout, stderr, code := run(c.Mkdir(), "help")
	c.Assert(stderr, qt.Matches, "")
	c.Assert(code, qt.Equals, 0)
	c.Assert(stdout, qt.Matches, `
(.|\n)*    foo                 - foo --description
(.|\n)*
`[1:])
}

func (s *pluginSuite) TestWhiteListIsExtensible(c *qt.C) {
	s.makeFullPlugin(pluginParams{Name: "foo"})
	s.makeFullPlugin(pluginParams{Name: "danger"})
	writePlugin(s.dir, "tools-commands", "#!/bin/bash --norc\necho [\"danger\",]", 0755)
	stdout, stderr, code := run(c.Mkdir(), "help")
	c.Assert(stderr, qt.Matches, "")
	c.Assert(code, qt.Equals, 0)
	c.Assert(stdout, qt.Matches, `
(.|\n)*    danger              - danger --description
(.|\n)*    foo                 - foo --description
(.|\n)*
`[1:])
}

func (s *pluginSuite) TestPluginCacheCaches(c *qt.C) {
	c.Setenv("HOME", "/tmp")
	s.makeFullPlugin(pluginParams{Name: "foo"})
	run(c.Mkdir(), "help")
	c.Assert(*charmcmd.PluginDescriptionLastCallReturnedCache, qt.Equals, false)
	charmcmd.ResetPluginDescriptionsResults()
	run(c.Mkdir(), "help")
	c.Assert(*charmcmd.PluginDescriptionLastCallReturnedCache, qt.Equals, true)
}

func (s *pluginSuite) TestPluginCacheInvalidatesOnUpdate(c *qt.C) {
	c.Setenv("HOME", "/tmp")
	s.makeFullPlugin(pluginParams{Name: "foo"})
	run(c.Mkdir(), "help")
	c.Assert(*charmcmd.PluginDescriptionLastCallReturnedCache, qt.Equals, false)
	charmcmd.ResetPluginDescriptionsResults()
	time.Sleep(time.Second) // Sleep so that the written file has a different mtime
	s.makeFullPlugin(pluginParams{Name: "foo"})
	run(c.Mkdir(), "help")
	c.Assert(*charmcmd.PluginDescriptionLastCallReturnedCache, qt.Equals, false)
}

func (s *pluginSuite) TestPluginCacheInvalidatesOnNewPlugin(c *qt.C) {
	c.Setenv("HOME", "/tmp")
	s.makeFullPlugin(pluginParams{Name: "foo"})
	run(c.Mkdir(), "help")
	c.Assert(*charmcmd.PluginDescriptionLastCallReturnedCache, qt.Equals, false)
	charmcmd.ResetPluginDescriptionsResults()
	s.makeFullPlugin(pluginParams{Name: "bar"})
	run(c.Mkdir(), "help")
	c.Assert(*charmcmd.PluginDescriptionLastCallReturnedCache, qt.Equals, false)
}

func (s *pluginSuite) TestPluginCacheInvalidatesRemovedPlugin(c *qt.C) {
	c.Setenv("HOME", "/tmp")
	s.makeFullPlugin(pluginParams{Name: "foo"})
	// Add bar so that there is more than one plugin. If no plugins are found
	// there is a short circuit which makes this test do the wrong thing.
	s.makeFullPlugin(pluginParams{Name: "bar"})
	run(c.Mkdir(), "help")
	charmcmd.ResetPluginDescriptionsResults()
	os.Remove(filepath.Join(s.dir, "charm-foo"))
	stdout, _, _ := run(c.Mkdir(), "help")
	// The qt.Matches checker anchors the regex by surrounding it with ^ and $
	// Checking for a not match this way instead.
	matches, err := regexp.MatchString(`
foo            - foo --description
`[1:], stdout)
	if err != nil {
		c.Log("regex error" + err.Error())
		c.Fail()
	}
	expected := false
	if matches != expected {
		c.Log("output did not match expected output:" + stdout)
	}
	c.Assert(matches, qt.Equals, expected)
}

func (s *pluginSuite) makePlugin(name string, perm os.FileMode) {
	content := fmt.Sprintf("#!/bin/bash --norc\necho %s $*", name)
	writePlugin(s.dir, name, content, perm)
}

func (s *pluginSuite) makeFailingPlugin(name string, exitStatus int) {
	content := fmt.Sprintf("#!/bin/bash --norc\necho failing\nexit %d", exitStatus)
	writePlugin(s.dir, name, content, 0755)
}

func (s *pluginSuite) makeFullPluginInSecondDir(params pluginParams) {
	makeFullPluginToDir(params, s.dir2)
}

func (s *pluginSuite) makeFullPlugin(params pluginParams) {
	makeFullPluginToDir(params, s.dir)
}

func makeFullPluginToDir(params pluginParams, dir string) {
	// Create a new template and parse the plugin into it.
	t := template.Must(template.New("plugin").Parse(pluginTemplate))
	content := &bytes.Buffer{}
	// Create the files in a temp dir, so we don't pollute the working space.
	if params.Creates != "" {
		params.Creates = filepath.Join(dir, params.Creates)
	}
	if params.DependsOn != "" {
		params.DependsOn = filepath.Join(dir, params.DependsOn)
	}
	if err := t.Execute(content, params); err != nil {
		panic(err)
	}
	writePlugin(dir, params.Name, content.String(), 0755)
}

func writePlugin(dir, name, content string, perm os.FileMode) {
	path := filepath.Join(dir, "charm-"+name)
	if err := ioutil.WriteFile(path, []byte(content), perm); err != nil {
		panic(err)
	}
}

type pluginParams struct {
	Name       string
	ExitStatus int
	Creates    string
	DependsOn  string
}

const pluginTemplate = `#!/bin/bash --norc

if [ "$1" = "--description" ]; then
  if [ -n "{{.Creates}}" ]; then
    /usr/bin/touch "{{.Creates}}"
  fi
  if [ -n "{{.DependsOn}}" ]; then
    # Sleep 10ms while waiting to allow other stuff to do work
    while [ ! -e "{{.DependsOn}}" ]; do /bin/sleep 0.010; done
  fi
  echo "{{.Name}} --description"
  exit {{.ExitStatus}}
fi

if [ "$1" = "--help" ]; then
  echo "{{.Name}} longer help"
  echo ""
  echo "something useful"
  exit {{.ExitStatus}}
fi

if [ "$1" = "--debug" ]; then
  echo "some debug"
  exit {{.ExitStatus}}
fi

if [ "$1" = "--unknown-to-juju" ]; then
  echo "the flag was still there."
  exit {{.ExitStatus}}
fi

echo {{.Name}} $*
echo "answer is $ANSWER"
exit {{.ExitStatus}}
`

func runHelp(c *qt.C) (string, string) {
	stdout, stderr, code := run(c.Mkdir(), "help", "plugins")
	c.Assert(code, qt.Equals, 0)
	c.Assert(strings.HasPrefix(stdout, charmcmd.PluginTopicText), qt.Equals, true)
	return stdout[len(charmcmd.PluginTopicText):], stderr
}
