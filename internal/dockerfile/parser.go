package dockerfile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/constants"
)

// ErrUnsupported marks Dockerfile content v1 deliberately refuses to interpret.
// Phase 1.4: unusual syntax fails safely with a clear message rather than a
// guess. Wrapped errors carry the line and the reason.
var ErrUnsupported = errors.New("unsupported Dockerfile")

// maxDockerfileBytes caps the read. Real Dockerfiles are a few kilobytes.
const maxDockerfileBytes = 1 << 20

// Instruction is one parsed Dockerfile instruction.
type Instruction struct {
	// Cmd is the instruction keyword, upper-cased: FROM, RUN, COPY, ...
	Cmd string

	// Flags are leading --name=value options (COPY --from=build).
	Flags map[string]string

	// Args is the instruction text after the keyword and flags, with line
	// continuations joined.
	Args string

	// Exec is set when Args is a JSON exec-form array.
	Exec []string

	// Line is the 1-based line the instruction starts on.
	Line int
}

// IsExec reports whether the instruction used JSON exec form.
func (i Instruction) IsExec() bool { return i.Exec != nil }

// Stage is one FROM ... block.
type Stage struct {
	// Name is the AS alias, or "".
	Name string

	// Image is the resolved base image reference (ARGs substituted).
	Image Image

	// FromStage is set when the base is an earlier stage by name.
	FromStage string

	// Instructions excludes the FROM itself.
	Instructions []Instruction
}

// Dockerfile is a parsed Dockerfile.
type Dockerfile struct {
	// Path is where it was read from.
	Path string

	// RelPath is Path relative to the repository root, slash-separated
	// ("Dockerfile", "build/Dockerfile").
	RelPath string

	// Stages in file order. Never empty for a successfully parsed file.
	Stages []Stage

	// GlobalArgs are ARGs declared before the first FROM, with defaults.
	GlobalArgs map[string]string

	// Lines is the physical line count, for reporting.
	Lines int

	// Raw is the original content.
	Raw []byte
}

// Final returns the last stage — the one that becomes the image.
func (d *Dockerfile) Final() *Stage { return &d.Stages[len(d.Stages)-1] }

// MultiStage reports whether there is more than one stage.
func (d *Dockerfile) MultiStage() bool { return len(d.Stages) > 1 }

// RuntimeImage resolves the final stage's base image, following FROM <stage>
// chains back to a real image.
func (d *Dockerfile) RuntimeImage() Image {
	s := d.Final()
	for depth := 0; s.FromStage != "" && depth < len(d.Stages); depth++ {
		next := d.stage(s.FromStage)
		if next == nil {
			break
		}
		s = next
	}
	return s.Image
}

func (d *Dockerfile) stage(name string) *Stage {
	for i := range d.Stages {
		if strings.EqualFold(d.Stages[i].Name, name) {
			return &d.Stages[i]
		}
	}
	return nil
}

// All returns every instruction with the given keyword across all stages.
func (d *Dockerfile) All(cmd string) []Instruction {
	var out []Instruction
	for _, s := range d.Stages {
		out = append(out, s.All(cmd)...)
	}
	return out
}

// All returns every instruction in the stage with the given keyword.
func (s *Stage) All(cmd string) []Instruction {
	var out []Instruction
	for _, in := range s.Instructions {
		if in.Cmd == cmd {
			out = append(out, in)
		}
	}
	return out
}

// Last returns the final instruction with the given keyword, or nil. For CMD,
// ENTRYPOINT, WORKDIR, USER and HEALTHCHECK only the last one matters.
func (s *Stage) Last(cmd string) *Instruction {
	for i := len(s.Instructions) - 1; i >= 0; i-- {
		if s.Instructions[i].Cmd == cmd {
			return &s.Instructions[i]
		}
	}
	return nil
}

// knownInstructions is the full Dockerfile reference. Anything else is a typo
// or a dialect this tool does not speak.
var knownInstructions = map[string]bool{
	"FROM": true, "RUN": true, "CMD": true, "LABEL": true, "MAINTAINER": true,
	"EXPOSE": true, "ENV": true, "ADD": true, "COPY": true, "ENTRYPOINT": true,
	"VOLUME": true, "USER": true, "WORKDIR": true, "ARG": true, "ONBUILD": true,
	"STOPSIGNAL": true, "HEALTHCHECK": true, "SHELL": true,
}

// conventionalDirs are where repositories commonly keep their Dockerfile when
// it is not at the root, built with the root as context
// (`docker build -f build/Dockerfile .`).
var conventionalDirs = []string{"build", "docker", "deploy", ".docker"}

// Parse finds and reads the repository's Dockerfile: <root>/Dockerfile, else
// the first of build/, docker/, deploy/, .docker/ that holds one. A missing
// Dockerfile is not an error: it returns (nil, nil) and the caller infers one
// from stack conventions (Phase 1.4).
//
// Without the conventional directories a repo with build/Dockerfile would be
// compared against an imaginary baseline instead of its real image, and the
// report's "before" would be fiction.
func Parse(root string) (*Dockerfile, error) {
	candidates := []string{constants.DockerfileName}
	for _, d := range conventionalDirs {
		candidates = append(candidates, d+"/"+constants.DockerfileName)
	}
	for _, rel := range candidates {
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
			return ParseFile(root, rel)
		}
	}
	return nil, nil
}

// ParseFile reads the Dockerfile at rel (slash-separated, relative to root).
// The path must stay inside root: it is user input pointing into an untrusted
// repository (Phase 9.1).
func ParseFile(root, rel string) (*Dockerfile, error) {
	clean := filepath.Clean(filepath.FromSlash(rel))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("dockerfile: %s is outside the repository", rel)
	}
	path := filepath.Join(root, clean)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("dockerfile: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("dockerfile: %s is not a regular file", rel)
	}
	if info.Size() > maxDockerfileBytes {
		return nil, fmt.Errorf("%w: %s is larger than 1 MiB", ErrUnsupported, rel)
	}
	raw, err := os.ReadFile(path) //nolint:gosec // root-joined, checked above
	if err != nil {
		return nil, fmt.Errorf("dockerfile: %w", err)
	}
	df, err := ParseBytes(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.ToSlash(clean), err)
	}
	df.Path = path
	df.RelPath = filepath.ToSlash(clean)
	return df, nil
}

var (
	directiveRe = regexp.MustCompile(`^#\s*([a-zA-Z]+)\s*=\s*(.+?)\s*$`)
	heredocRe   = regexp.MustCompile(`<<-?\s*["']?[A-Za-z_][A-Za-z0-9_]*["']?`)
	varRe       = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(:?[-+])?([^}]*)\}|\$([A-Za-z_][A-Za-z0-9_]*)`)
)

// ParseBytes parses Dockerfile content.
//
// Supported: comments, parser directives (syntax, escape), line continuations,
// JSON exec form, ARG substitution in FROM, named and numbered stages.
// Refused with ErrUnsupported: heredocs, ONBUILD, unknown instructions, and
// a FROM whose image cannot be resolved without a build argument.
func ParseBytes(raw []byte) (*Dockerfile, error) {
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	text = strings.TrimPrefix(text, string(rune(0xFEFF))) // UTF-8 BOM from Windows editors
	lines := strings.Split(text, "\n")

	df := &Dockerfile{Raw: raw, Lines: len(lines), GlobalArgs: map[string]string{}}
	escape := byte('\\')

	// Parser directives are only honoured before the first non-directive
	// line, per the Dockerfile reference.
	directives := true

	var cur strings.Builder
	start := 0
	flush := func() error {
		logical := strings.TrimSpace(cur.String())
		cur.Reset()
		if logical == "" {
			return nil
		}
		return df.addInstruction(logical, start)
	}

	for i, line := range lines {
		lineNo := i + 1
		trimmed := strings.TrimSpace(line)

		if cur.Len() == 0 {
			if directives {
				if m := directiveRe.FindStringSubmatch(trimmed); m != nil {
					if strings.EqualFold(m[1], "escape") {
						if m[2] != "\\" && m[2] != "`" {
							return nil, fmt.Errorf("%w: line %d: escape directive must be \\ or `", ErrUnsupported, lineNo)
						}
						escape = m[2][0]
					}
					continue
				}
				if trimmed != "" {
					directives = false
				}
			}
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			start = lineNo
		} else if strings.HasPrefix(trimmed, "#") {
			// Comment lines inside a continuation are dropped, as Docker does.
			continue
		}

		if strings.HasSuffix(trimmed, string(escape)) {
			cur.WriteString(strings.TrimRight(strings.TrimSuffix(trimmed, string(escape)), " \t"))
			cur.WriteByte(' ')
			continue
		}
		cur.WriteString(trimmed)
		if err := flush(); err != nil {
			return nil, err
		}
	}
	if cur.Len() > 0 {
		// A trailing continuation at EOF: Docker accepts it, and so do we.
		if err := flush(); err != nil {
			return nil, err
		}
	}

	if len(df.Stages) == 0 {
		return nil, fmt.Errorf("%w: no FROM instruction", ErrUnsupported)
	}
	return df, nil
}

func (d *Dockerfile) addInstruction(logical string, line int) error {
	keyword, rest, _ := strings.Cut(logical, " ")
	cmd := strings.ToUpper(keyword)
	rest = strings.TrimSpace(rest)

	if !knownInstructions[cmd] {
		return fmt.Errorf("%w: line %d: unknown instruction %q", ErrUnsupported, line, keyword)
	}
	if cmd == "ONBUILD" {
		return fmt.Errorf("%w: line %d: ONBUILD triggers are not supported", ErrUnsupported, line)
	}
	if (cmd == "RUN" || cmd == "COPY" || cmd == "ADD") && heredocRe.MatchString(rest) {
		return fmt.Errorf("%w: line %d: heredoc syntax is not supported", ErrUnsupported, line)
	}

	in := Instruction{Cmd: cmd, Line: line, Flags: map[string]string{}}
	for strings.HasPrefix(rest, "--") {
		flag, after, _ := strings.Cut(rest, " ")
		name, value, _ := strings.Cut(strings.TrimPrefix(flag, "--"), "=")
		in.Flags[strings.ToLower(name)] = value
		rest = strings.TrimSpace(after)
	}
	in.Args = rest

	if strings.HasPrefix(rest, "[") {
		var exec []string
		if err := json.Unmarshal([]byte(rest), &exec); err == nil {
			in.Exec = exec
		}
	}

	if cmd == "FROM" {
		return d.addStage(in)
	}
	if len(d.Stages) == 0 {
		if cmd == "ARG" {
			for name, val := range parseArg(in.Args) {
				d.GlobalArgs[name] = val
			}
			return nil
		}
		return fmt.Errorf("%w: line %d: %s before the first FROM", ErrUnsupported, line, cmd)
	}
	s := &d.Stages[len(d.Stages)-1]
	s.Instructions = append(s.Instructions, in)
	return nil
}

func (d *Dockerfile) addStage(in Instruction) error {
	fields := strings.Fields(in.Args)
	if len(fields) == 0 {
		return fmt.Errorf("%w: line %d: FROM without an image", ErrUnsupported, in.Line)
	}
	ref, err := d.expand(fields[0], in.Line)
	if err != nil {
		return err
	}
	st := Stage{}
	if len(fields) >= 3 && strings.EqualFold(fields[1], "AS") {
		st.Name = fields[2]
	}
	if d.stage(ref) != nil {
		st.FromStage = ref
	} else {
		st.Image = ParseImage(ref)
	}
	d.Stages = append(d.Stages, st)
	return nil
}

// expand substitutes ${VAR}, ${VAR:-default} and $VAR from global ARGs. A
// variable with no value is a build-time input this tool cannot see, so the
// image is unknowable and the parse refuses rather than guessing.
func (d *Dockerfile) expand(s string, line int) (string, error) {
	var missing string
	out := varRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := varRe.FindStringSubmatch(m)
		name, op, def := sub[1], sub[2], sub[3]
		if name == "" {
			name = sub[4]
		}
		v, set := d.GlobalArgs[name]
		switch {
		case strings.HasSuffix(op, "+"):
			// ${VAR:+alt}: alt when set, empty otherwise.
			if set && v != "" {
				return def
			}
			return ""
		case set && v != "":
			return v
		case strings.HasSuffix(op, "-"):
			return def
		}
		missing = name
		return ""
	})
	if missing != "" {
		return "", fmt.Errorf("%w: line %d: base image depends on build argument %s, which has no default", ErrUnsupported, line, missing)
	}
	return out, nil
}

// parseArg reads `ARG NAME` or `ARG NAME=value`, possibly several per line.
func parseArg(args string) map[string]string {
	out := map[string]string{}
	for _, f := range strings.Fields(args) {
		name, val, _ := strings.Cut(f, "=")
		out[name] = strings.Trim(val, `"'`)
	}
	return out
}

// Ports returns every EXPOSEd port of the stage, without protocol suffixes,
// skipping anything that references a variable.
func (s *Stage) Ports() []string {
	var out []string
	for _, in := range s.All("EXPOSE") {
		for _, f := range strings.Fields(in.Args) {
			p, _, _ := strings.Cut(f, "/")
			if p != "" && !strings.Contains(p, "$") {
				out = append(out, p)
			}
		}
	}
	return out
}

// Env returns the stage's ENV pairs in order. Both `ENV K=V K2=V2` and the
// legacy `ENV K V` form are handled.
func (s *Stage) Env() [][2]string {
	var out [][2]string
	for _, in := range s.All("ENV") {
		out = append(out, ParseEnv(in.Args)...)
	}
	return out
}

// ParseEnv parses the argument text of one ENV instruction.
func ParseEnv(args string) [][2]string {
	args = strings.TrimSpace(args)
	first, rest, _ := strings.Cut(args, " ")
	if !strings.Contains(first, "=") {
		// Legacy form: ENV KEY value with spaces
		return [][2]string{{first, strings.TrimSpace(rest)}}
	}
	var out [][2]string
	for _, tok := range splitShellWords(args) {
		k, v, ok := strings.Cut(tok, "=")
		if ok {
			out = append(out, [2]string{k, v})
		}
	}
	return out
}

// splitShellWords splits on unquoted whitespace and strips one level of
// quoting. Enough for ENV and LABEL; not a shell.
func splitShellWords(s string) []string {
	var out []string
	var cur strings.Builder
	var quote rune
	inWord := false
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote = r
			inWord = true
		case r == ' ' || r == '\t':
			if inWord {
				out = append(out, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if inWord {
		out = append(out, cur.String())
	}
	return out
}
