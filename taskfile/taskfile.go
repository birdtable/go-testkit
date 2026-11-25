// Package taskfile holds a repository's Taskfile.yml to Go: a task's commands call a Go
// program, or one binary with its arguments, and keep their logic, paths and temporary folders in Go,
// where one host's vet, build and lint for each system already read them. go-task runs the shell of
// Taskfile.yml through its own interpreter, which runs it differently on Windows (an external
// program's output appended with >> fails there, and its tar takes no -C), and nothing else reads it.
//
// A repository calls Check from a test in its commit tier, with the folder that holds its
// Taskfile.yml:
//
//	func TestTheTaskfileCallsGo(t *testing.T) {
//		t.Parallel()
//		taskfile.Check(t, "../..")
//	}
//
// Check reads each command of each task's cmds (a cmd, a plain command and a deferred command) as
// go-task's interpreter would, with mvdan.cc/sh's parser, and fails the test for each of these in it:
//
//   - a shell utility run as a command: mktemp, grep, sed, awk, cut, tar, find or xargs;
//   - a pipe;
//   - a redirection, but for one to or from the null device (/dev/null, or NUL) and for one that only
//     points a descriptor at another (2>&1, >&2); a here-document is a redirection too;
//   - a for loop in shell (go-task's own for: is Go, and is not one);
//   - a /tmp path;
//   - a path joined in shell, a variable's or a template's value with / and more after it ($d/x,
//     {{.ROOT_DIR}}/x), where Go would join it with path/filepath.
//
// Before a command is read, each {{.NAME}} whose NAME is a plain string under the file's vars or the
// task's own is replaced by its value, as go-task would, so a var that holds shell is read where it is
// used and a var that names a tool reads as the literal path it is. Any other template, as go-task's
// own variables ({{.ROOT_DIR}}) or a var set by sh:, reads as a variable.
//
// A command the repository keeps in shell, with its reason, is a line of taskfile-exceptions.txt beside
// Taskfile.yml: the task's name, the command's place in its cmds counted from 1, and the reason, split
// by tabs. A line that starts with # and a blank line are not read. The line passes everything the
// check finds in that command. A line that names no command, or a command where the check finds
// nothing, fails the test too, so the file lists what is still shell and no more.
//
// It is built on mvdan.cc/sh's parser, the one go-task's interpreter is built on, and on
// go.yaml.in/yaml/v3, and adds the check go-task leaves out: that a command keeps no logic in shell.
// The repository configures the folder it passes to Check and its taskfile-exceptions.txt.
//
// Not read: a task's preconditions, status, sources and generates, a var's sh:, an included Taskfile,
// and a script the command runs or sources.
package taskfile

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"maps"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
	"mvdan.cc/sh/v3/syntax"
)

// The files Check reads, in the folder it is given.
const (
	taskfileName   = "Taskfile.yml"
	exceptionsName = "taskfile-exceptions.txt"
)

// Check fails t for each command of root's Taskfile.yml that is shell the package doc names, and is
// not passed by a line of root's taskfile-exceptions.txt, and for each line of that file that names
// no such command. A Taskfile.yml it cannot read fails t at once.
func Check(t testing.TB, root string) {
	t.Helper()
	refused, err := read(root)
	if err != nil {
		t.Fatalf("taskfile: %v", err)
		return
	}
	for _, r := range refused {
		t.Error(r)
	}
}

// command is one command of a task, as Taskfile.yml holds it.
type command struct {
	task string // the task's name
	n    int    // its place in the task's cmds, from 1
	line int    // its line in Taskfile.yml
	text string // the command as written
	vars map[string]string
}

// exception is one line of taskfile-exceptions.txt.
type exception struct {
	task string
	n    int
	line int // its line in the file
	used bool
}

// read gives what root's files break, one line of text each, in the files' order. It reads both
// files through an os.Root of root, so neither read leaves the folder, by a link or otherwise.
func read(root string) ([]string, error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	refused, err := readIn(r)
	if err = errors.Join(err, r.Close()); err != nil {
		return nil, err
	}
	return refused, nil
}

// readIn is read's work, in the opened root r.
func readIn(r *os.Root) ([]string, error) {
	b, err := r.ReadFile(taskfileName)
	if err != nil {
		return nil, err
	}
	cmds, err := commands(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", taskfileName, err)
	}
	excepted, refused, err := exceptions(r)
	if err != nil {
		return nil, err
	}
	for _, c := range cmds {
		found := shapes(c)
		ex := excepted[key(c.task, c.n)]
		if ex != nil {
			ex.used = true
			if len(found) == 0 {
				refused = append(refused, fmt.Sprintf("%s:%d: task %s, command %d (%s): the check finds nothing in it, so the line is not needed",
					exceptionsName, ex.line, c.task, c.n, first(c.text)))
			}
			continue
		}
		for _, f := range found {
			refused = append(refused, fmt.Sprintf("%s:%d: task %s, command %d (%s): %s", taskfileName, c.line, c.task, c.n, first(c.text), f))
		}
	}
	for _, ex := range sorted(excepted) {
		if !ex.used {
			refused = append(refused, fmt.Sprintf("%s:%d: task %s has no command %d that runs shell", exceptionsName, ex.line, ex.task, ex.n))
		}
	}
	return refused, nil
}

// key is the map key of a task's command.
func key(task string, n int) string { return task + "\t" + strconv.Itoa(n) }

// sorted gives the exceptions in the order of their lines.
func sorted(m map[string]*exception) []*exception {
	out := make([]*exception, 0, len(m))
	for _, ex := range m {
		out = append(out, ex)
	}
	slices.SortFunc(out, func(a, b *exception) int { return a.line - b.line })
	return out
}

// first gives a command's first line that is not blank, cut to 60 bytes, to name it in a message.
func first(text string) string {
	for l := range strings.SplitSeq(text, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if len(l) > 60 {
			l = l[:57] + "..."
		}
		return l
	}
	return ""
}

// exceptions reads the exceptions file in r, which may be missing. Its malformed lines come back as
// refusals.
func exceptions(r *os.Root) (map[string]*exception, []string, error) {
	b, err := r.ReadFile(exceptionsName)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]*exception{}, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	out := map[string]*exception{}
	var refused []string
	s := bufio.NewScanner(bytes.NewReader(b))
	for n := 1; s.Scan(); n++ {
		l := strings.TrimSuffix(s.Text(), "\r")
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		f := strings.Split(l, "\t")
		place := 0
		if len(f) == 3 {
			place, _ = strconv.Atoi(f[1]) //nolint:errcheck // a place that does not parse is 0, refused below
		}
		switch {
		case place < 1 || f[0] == "" || strings.TrimSpace(f[2]) == "":
			refused = append(refused, fmt.Sprintf("%s:%d: the line is not a task, a command's place from 1 and a reason, tab-separated: %q", exceptionsName, n, l))
		case out[key(f[0], place)] != nil:
			refused = append(refused, fmt.Sprintf("%s:%d: task %s, command %d is listed on line %d already", exceptionsName, n, f[0], place, out[key(f[0], place)].line))
		default:
			out[key(f[0], place)] = &exception{task: f[0], n: place, line: n}
		}
	}
	return out, refused, s.Err()
}

// commands gives every command of every task in Taskfile.yml's bytes b, in the file's order.
func commands(b []byte) ([]command, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 || resolve(doc.Content[0]).Kind != yaml.MappingNode {
		return nil, errors.New("the file is not a mapping of go-task's keys")
	}
	top := resolve(doc.Content[0])
	global := plainVars(get(top, "vars"))
	tasks := get(top, "tasks")
	if tasks == nil {
		return nil, nil
	}
	if tasks.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("line %d: tasks is not a mapping", tasks.Line)
	}
	var out []command
	for i := 0; i+1 < len(tasks.Content); i += 2 {
		name, body := tasks.Content[i].Value, resolve(tasks.Content[i+1])
		vars := global
		var list *yaml.Node
		switch body.Kind {
		case yaml.ScalarNode, yaml.SequenceNode: // go-task's short forms: one command, or the list of them
			list = body
		case yaml.MappingNode:
			list = get(body, "cmds")
			vars = merged(global, plainVars(get(body, "vars")))
		default:
		}
		out = append(out, taskCommands(name, list, vars)...)
	}
	return out, nil
}

// taskCommands gives the commands of the task name's cmds, list.
func taskCommands(name string, list *yaml.Node, vars map[string]string) []command {
	if list == nil {
		return nil
	}
	items := []*yaml.Node{list}
	if list.Kind == yaml.SequenceNode {
		items = list.Content
	}
	var out []command
	for i, item := range items {
		text := shellOf(resolve(item))
		if text == nil {
			continue
		}
		out = append(out, command{task: name, n: i + 1, line: text.Line, text: text.Value, vars: vars})
	}
	return out
}

// shellOf gives the node of the shell an item of cmds runs, or nil for one that runs none (a task:, or
// a deferred task).
func shellOf(item *yaml.Node) *yaml.Node {
	switch item.Kind {
	case yaml.ScalarNode:
		return item
	case yaml.MappingNode:
		for _, k := range []string{"cmd", "defer"} {
			if v := get(item, k); v != nil && v.Kind == yaml.ScalarNode {
				return v
			}
		}
	default:
	}
	return nil
}

// resolve follows an alias to the node it names.
func resolve(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}

// get gives the value of key in the mapping m, or nil.
func get(m *yaml.Node, key string) *yaml.Node {
	m = resolve(m)
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return resolve(m.Content[i+1])
		}
	}
	return nil
}

// plainVars gives the vars of the mapping m whose values are plain strings, as go-task puts them into a
// template: a var set by sh:, or by any other mapping, is left out.
func plainVars(m *yaml.Node) map[string]string {
	out := map[string]string{}
	if m == nil || m.Kind != yaml.MappingNode {
		return out
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if v := resolve(m.Content[i+1]); v.Kind == yaml.ScalarNode {
			out[m.Content[i].Value] = v.Value
		}
	}
	return out
}

// merged gives a's vars with b's over them.
func merged(a, b map[string]string) map[string]string {
	out := maps.Clone(a)
	maps.Copy(out, b)
	return out
}

// The templates in a command: a plain var's, and any.
var (
	plainVar = regexp.MustCompile(`\{\{-?\s*\.([A-Za-z_][A-Za-z0-9_]*)\s*-?\}\}`)
	anyTmpl  = regexp.MustCompile(`(?s)\{\{(?:"(?:[^"\\]|\\.)*"|` + "`[^`]*`" + `|.)*?\}\}`) // a string in it may hold }}
	tmpPath  = regexp.MustCompile(`(^|[^A-Za-z0-9_.~/-])/tmp(/|$|[^A-Za-z0-9_.-])`)
)

// templateVar is the variable a template other than a plain var's reads as.
const templateVar = "$GO_TASK_TEMPLATE"

// rendered gives c's text with each plain var put in, and each other template read as a variable.
func rendered(c command) string {
	s := c.text
	for range 8 { // a var may name another; go-task's own depth is not reached by any repository's file
		next := plainVar.ReplaceAllStringFunc(s, func(m string) string {
			if v, ok := c.vars[plainVar.FindStringSubmatch(m)[1]]; ok {
				return v
			}
			return m
		})
		if next == s {
			break
		}
		s = next
	}
	return anyTmpl.ReplaceAllLiteralString(s, templateVar)
}

// shapes gives what c holds of the shapes the package doc names, each said once, in the order the
// parser meets them.
func shapes(c command) []string {
	src := rendered(c)
	f, err := syntax.NewParser().Parse(strings.NewReader(src), "")
	if err != nil {
		return []string{"go-task's interpreter would not read it as shell: " + err.Error()}
	}
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	text := func(n syntax.Node) string {
		a, b := int(n.Pos().Offset()), int(n.End().Offset())
		return first(src[a:min(b, len(src))])
	}
	syntax.Walk(f, func(n syntax.Node) bool {
		if s := shape(n, text); s != "" {
			add(s)
		}
		return true
	})
	return out
}

// shape names the shape the node n is, or gives "". text gives a node's source, to quote it.
func shape(n syntax.Node, text func(syntax.Node) string) string {
	switch x := n.(type) {
	case *syntax.CallExpr:
		if len(x.Args) > 0 {
			w := x.Args[0].Lit()
			if base := w[strings.LastIndexAny(w, `/\`)+1:]; utility(base) {
				return "the shell utility " + base
			}
		}
	case *syntax.BinaryCmd:
		if x.Op == syntax.Pipe || x.Op == syntax.PipeAll {
			return "a pipe (" + text(x) + ")"
		}
	case *syntax.Redirect:
		if !harmless(x) {
			return "a redirection (" + text(x) + ")"
		}
	case *syntax.ForClause:
		return "a for loop in shell"
	case *syntax.Lit:
		if tmpPath.MatchString(x.Value) {
			return "a /tmp path (" + x.Value + ")"
		}
	case *syntax.SglQuoted:
		if tmpPath.MatchString(x.Value) {
			return "a /tmp path ('" + first(x.Value) + "')"
		}
	case *syntax.Word:
		if joined(x.Parts) {
			return "a path joined in shell (" + text(x) + ")"
		}
	}
	return ""
}

// utility is whether name is one of the shell utilities the package doc names.
func utility(name string) bool {
	switch name {
	case "mktemp", "grep", "sed", "awk", "cut", "tar", "find", "xargs":
		return true
	}
	return false
}

// harmless is whether r is a redirection to or from the null device, or one that points a descriptor
// at another.
func harmless(r *syntax.Redirect) bool {
	target := ""
	if r.Word != nil {
		target = r.Word.Lit()
	}
	switch r.Op { //nolint:exhaustive // every other operator is refused
	case syntax.RdrOut, syntax.AppOut, syntax.RdrIn, syntax.RdrAll, syntax.AppAll, syntax.RdrClob:
		return target == "/dev/null" || strings.EqualFold(target, "NUL")
	case syntax.DplOut, syntax.DplIn:
		_, err := strconv.Atoi(target)
		return err == nil || target == "-"
	}
	return false
}

// joined is whether the parts of a word put a variable's value, a command's output or a template's
// value before a / and more of the word.
func joined(parts []syntax.WordPart) bool {
	for i, p := range parts {
		if q, ok := p.(*syntax.DblQuoted); ok && joined(q.Parts) {
			return true
		}
		if i+1 < len(parts) && expands(p) && strings.HasPrefix(leading(parts[i+1]), "/") {
			return true
		}
	}
	return false
}

// expands is whether the part p is, or ends in, a value the shell puts in.
func expands(p syntax.WordPart) bool {
	switch x := p.(type) {
	case *syntax.ParamExp, *syntax.CmdSubst, *syntax.ArithmExp:
		return true
	case *syntax.DblQuoted:
		return len(x.Parts) > 0 && expands(x.Parts[len(x.Parts)-1])
	}
	return false
}

// leading gives the literal text the part p starts with.
func leading(p syntax.WordPart) string {
	switch x := p.(type) {
	case *syntax.Lit:
		return x.Value
	case *syntax.SglQuoted:
		return x.Value
	case *syntax.DblQuoted:
		if len(x.Parts) > 0 {
			return leading(x.Parts[0])
		}
	}
	return ""
}
