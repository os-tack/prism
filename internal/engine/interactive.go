package engine

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"agents.dev/agents/internal/model"
)

// ErrInteractiveDeclinedAll is returned by filterProjectInteractively when
// the user declines every item, leaving nothing to serialize.
var ErrInteractiveDeclinedAll = errors.New("engine: interactive selection declined all items")

// ErrInteractiveNoTTY is returned when --interactive is requested but stdin
// is not a character device (e.g., piped input, CI).
var ErrInteractiveNoTTY = errors.New("engine: --interactive requires a TTY on stdin")

type promptMode int

const (
	modePerItem promptMode = iota
	modeAcceptAll
	modeDeclineAll
)

type askResult int

const (
	resultInclude askResult = iota
	resultExclude
)

// stdinIsTTY reports whether stdin is connected to a character device.
// Used by initProject to fail fast when --interactive is incompatible with
// the current invocation (pipe, redirect, CI).
func stdinIsTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// filterProjectInteractively walks the imported project and prompts the user
// for each top-level item, returning a project narrowed to the user's
// selections. EOF on the reader is treated as accept-all for the remaining
// items, so a hung pipe or Ctrl-D mid-session preserves the user's existing
// inclusions rather than silently dropping work.
//
// Concrete CLI behavior worth knowing about up-front:
//
//   - Non-TTY stdin is rejected at the cmd/agents/init.go boundary (see
//     stdinIsTTY and ErrInteractiveNoTTY) — piped or CI input never
//     reaches this function. If you need scriptable selection, generate
//     the project YAML directly rather than driving --interactive.
//   - Within an interactive session, EOF on stdin is treated as
//     accept-all for everything that has not yet been prompted. This
//     means:
//   - Ctrl-D at any prompt accepts the remaining items.
//   - Piping input that lacks a trailing newline causes the last
//     line to be consumed without a decision and EOF to fire on the
//     next prompt — which then accepts that item and every later
//     one. (We surface "(EOF -- accepting all remaining items)" to
//     stdout so the user can see this happen.)
//   - A closed pipe mid-stream similarly preserves whatever has not
//     been explicitly declined.
//     The intent is to fail safe: dropping imported content on a
//     stream-end accident is worse than over-including, since the user
//     can re-run with --interactive to narrow down further.
func filterProjectInteractively(p *model.Project, in *bufio.Reader, out io.Writer) (*model.Project, error) {
	if p == nil {
		return nil, fmt.Errorf("engine: nil project")
	}
	mode := modePerItem

	keptScopes, droppedScopePaths, scopeSkipSkills, err := filterScopes(p.Scopes, in, out, &mode)
	if err != nil {
		return nil, err
	}
	p.Scopes = keptScopes

	p.Skills, err = filterSkills(p.Skills, droppedScopePaths, scopeSkipSkills, in, out, &mode)
	if err != nil {
		return nil, err
	}
	p.Commands, err = filterCommands(p.Commands, droppedScopePaths, in, out, &mode)
	if err != nil {
		return nil, err
	}
	p.Agents, err = filterAgents(p.Agents, droppedScopePaths, in, out, &mode)
	if err != nil {
		return nil, err
	}
	p.MCP, err = filterMCP(p.MCP, droppedScopePaths, in, out, &mode)
	if err != nil {
		return nil, err
	}

	if isProjectEmpty(p) {
		return nil, ErrInteractiveDeclinedAll
	}
	return p, nil
}

func isProjectEmpty(p *model.Project) bool {
	if p.Context != nil && p.Context.Body != "" {
		return false
	}
	return len(p.Scopes) == 0 &&
		len(p.Skills) == 0 &&
		len(p.Commands) == 0 &&
		len(p.Agents) == 0 &&
		len(p.MCP) == 0
}

func filterScopes(scopes []*model.Scope, in *bufio.Reader, out io.Writer, mode *promptMode) ([]*model.Scope, map[string]struct{}, map[string]struct{}, error) {
	dropped := make(map[string]struct{})
	skipSkills := make(map[string]struct{})
	kept := make([]*model.Scope, 0, len(scopes))
	for _, s := range scopes {
		src := scopeSource(s)
		res, err := askWithExtras(in, out, mode, fmt.Sprintf("include scope %q (path: %s)? [Y/n/a/d/s]", s.Path, src), true)
		if err != nil {
			return nil, nil, nil, err
		}
		switch res.code {
		case resultInclude:
			kept = append(kept, s)
			if res.skipChildren {
				skipSkills[s.Path] = struct{}{}
			}
		case resultExclude:
			dropped[s.Path] = struct{}{}
		}
	}
	return kept, dropped, skipSkills, nil
}

func filterSkills(skills []*model.Skill, droppedScopes, skipSkillsForScope map[string]struct{}, in *bufio.Reader, out io.Writer, mode *promptMode) ([]*model.Skill, error) {
	kept := make([]*model.Skill, 0, len(skills))
	for _, s := range skills {
		if _, dropped := droppedScopes[s.ScopePath]; dropped {
			continue
		}
		if _, skipAll := skipSkillsForScope[s.ScopePath]; skipAll {
			continue
		}
		src := docSource(s.Document)
		label := s.Name
		if s.ScopePath != "" {
			label = s.ScopePath + "/" + s.Name
		}
		res, err := ask(in, out, mode, fmt.Sprintf("include skill %q (path: %s)? [Y/n/a/d]", label, src))
		if err != nil {
			return nil, err
		}
		if res == resultInclude {
			kept = append(kept, s)
		}
	}
	return kept, nil
}

func filterCommands(cmds []*model.Command, droppedScopes map[string]struct{}, in *bufio.Reader, out io.Writer, mode *promptMode) ([]*model.Command, error) {
	kept := make([]*model.Command, 0, len(cmds))
	for _, c := range cmds {
		if _, dropped := droppedScopes[c.ScopePath]; dropped {
			continue
		}
		src := docSource(c.Document)
		label := c.Name
		if c.ScopePath != "" {
			label = c.ScopePath + "/" + c.Name
		}
		res, err := ask(in, out, mode, fmt.Sprintf("include command %q (path: %s)? [Y/n/a/d]", label, src))
		if err != nil {
			return nil, err
		}
		if res == resultInclude {
			kept = append(kept, c)
		}
	}
	return kept, nil
}

func filterAgents(agents []*model.Agent, droppedScopes map[string]struct{}, in *bufio.Reader, out io.Writer, mode *promptMode) ([]*model.Agent, error) {
	kept := make([]*model.Agent, 0, len(agents))
	for _, a := range agents {
		if _, dropped := droppedScopes[a.ScopePath]; dropped {
			continue
		}
		src := docSource(a.Document)
		label := a.Name
		if a.ScopePath != "" {
			label = a.ScopePath + "/" + a.Name
		}
		res, err := ask(in, out, mode, fmt.Sprintf("include agent %q (path: %s)? [Y/n/a/d]", label, src))
		if err != nil {
			return nil, err
		}
		if res == resultInclude {
			kept = append(kept, a)
		}
	}
	return kept, nil
}

func filterMCP(servers []*model.MCPServer, droppedScopes map[string]struct{}, in *bufio.Reader, out io.Writer, mode *promptMode) ([]*model.MCPServer, error) {
	kept := make([]*model.MCPServer, 0, len(servers))
	for _, m := range servers {
		if _, dropped := droppedScopes[m.ScopePath]; dropped {
			continue
		}
		label := m.Name
		if m.ScopePath != "" {
			label = m.ScopePath + "/" + m.Name
		}
		res, err := ask(in, out, mode, fmt.Sprintf("include MCP server %q? [Y/n/a/d]", label))
		if err != nil {
			return nil, err
		}
		if res == resultInclude {
			kept = append(kept, m)
		}
	}
	return kept, nil
}

func scopeSource(s *model.Scope) string {
	if s == nil {
		return "(unknown)"
	}
	if src := docSource(s.Document); src != "(unknown)" {
		return src
	}
	return s.Path
}

func docSource(d *model.Document) string {
	if d == nil || d.SourcePath == "" {
		return "(unknown)"
	}
	return d.SourcePath
}

type extraResult struct {
	code         askResult
	skipChildren bool
}

// ask prompts once for an include/exclude decision, honoring an existing
// accept-all / decline-all mode set by an earlier 'a' or 'd' response.
func ask(in *bufio.Reader, out io.Writer, mode *promptMode, prompt string) (askResult, error) {
	switch *mode {
	case modeAcceptAll:
		return resultInclude, nil
	case modeDeclineAll:
		return resultExclude, nil
	}
	for {
		fmt.Fprintf(out, "%s ", prompt)
		line, err := in.ReadString('\n')
		if errors.Is(err, io.EOF) {
			// Treat EOF as accept-all so abandoned interactive sessions
			// preserve imported content rather than silently dropping it.
			*mode = modeAcceptAll
			fmt.Fprintln(out, "(EOF -- accepting all remaining items)")
			return resultInclude, nil
		}
		if err != nil {
			return resultInclude, fmt.Errorf("engine: read prompt input: %w", err)
		}
		answer := strings.TrimSpace(strings.ToLower(line))
		switch answer {
		case "", "y", "yes":
			return resultInclude, nil
		case "n", "no":
			return resultExclude, nil
		case "a", "all":
			*mode = modeAcceptAll
			return resultInclude, nil
		case "d", "decline", "none":
			*mode = modeDeclineAll
			return resultExclude, nil
		default:
			fmt.Fprintln(out, "  (Y=include, n=skip, a=accept all remaining, d=decline all remaining)")
		}
	}
}

// askWithExtras is ask plus an 's' answer (scope-only) that includes the
// scope but skips every skill nested under it.
func askWithExtras(in *bufio.Reader, out io.Writer, mode *promptMode, prompt string, allowSkipChildren bool) (extraResult, error) {
	switch *mode {
	case modeAcceptAll:
		return extraResult{code: resultInclude}, nil
	case modeDeclineAll:
		return extraResult{code: resultExclude}, nil
	}
	for {
		fmt.Fprintf(out, "%s ", prompt)
		line, err := in.ReadString('\n')
		if errors.Is(err, io.EOF) {
			*mode = modeAcceptAll
			fmt.Fprintln(out, "(EOF -- accepting all remaining items)")
			return extraResult{code: resultInclude}, nil
		}
		if err != nil {
			return extraResult{}, fmt.Errorf("engine: read prompt input: %w", err)
		}
		answer := strings.TrimSpace(strings.ToLower(line))
		switch answer {
		case "", "y", "yes":
			return extraResult{code: resultInclude}, nil
		case "n", "no":
			return extraResult{code: resultExclude}, nil
		case "a", "all":
			*mode = modeAcceptAll
			return extraResult{code: resultInclude}, nil
		case "d", "decline", "none":
			*mode = modeDeclineAll
			return extraResult{code: resultExclude}, nil
		case "s", "skip-skills":
			if allowSkipChildren {
				return extraResult{code: resultInclude, skipChildren: true}, nil
			}
			fmt.Fprintln(out, "  (Y=include, n=skip, a=accept all remaining, d=decline all remaining)")
		default:
			if allowSkipChildren {
				fmt.Fprintln(out, "  (Y=include, n=skip, a=accept all, d=decline all, s=include scope but skip its skills)")
			} else {
				fmt.Fprintln(out, "  (Y=include, n=skip, a=accept all remaining, d=decline all remaining)")
			}
		}
	}
}
