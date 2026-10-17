package dockerfile

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
)

// File is a Dockerfile under construction. The rule engine builds one; Generate
// renders it. Nothing here touches the filesystem — writing is the CLI's
// decision alone, behind -write or a confirmation (FR-5).
type File struct {
	// Header lines are rendered as comments at the top.
	Header []string

	// Syntax, when set, emits a `# syntax=` parser directive first.
	Syntax string

	Stages []*StageSpec
}

// StageSpec is one stage under construction.
type StageSpec struct {
	From    string
	Name    string
	Comment []string
	lines   []line
}

type line struct {
	comment []string
	cmd     string
	args    string
	gap     bool
}

// AddStage appends a stage and returns it for chaining.
func (f *File) AddStage(from, name string, comment ...string) *StageSpec {
	s := &StageSpec{From: from, Name: name, Comment: comment}
	f.Stages = append(f.Stages, s)
	return s
}

// Add appends an instruction.
func (s *StageSpec) Add(cmd, args string) *StageSpec {
	s.lines = append(s.lines, line{cmd: cmd, args: args})
	return s
}

// Note appends an instruction preceded by an explanatory comment and a blank
// line. Comments make the output reviewable: a user deciding whether to trust
// a generated file needs to see why each line is there.
func (s *StageSpec) Note(comment, cmd, args string) *StageSpec {
	s.lines = append(s.lines, line{comment: splitComment(comment), cmd: cmd, args: args, gap: true})
	return s
}

// Exec appends an instruction in JSON exec form.
func (s *StageSpec) Exec(cmd string, argv []string) *StageSpec {
	return s.Add(cmd, ExecForm(argv))
}

// Raw appends a carried-over instruction verbatim, with a comment.
func (s *StageSpec) Raw(comment string, in Instruction) *StageSpec {
	args := in.Args
	if len(in.Flags) > 0 {
		keys := make([]string, 0, len(in.Flags))
		for k := range in.Flags {
			keys = append(keys, k)
		}
		sort.Strings(keys) // deterministic output: same repo, same bytes
		var flags []string
		for _, k := range keys {
			v := in.Flags[k]
			if v == "" {
				flags = append(flags, "--"+k)
			} else {
				flags = append(flags, "--"+k+"="+v)
			}
		}
		args = strings.Join(flags, " ") + " " + args
	}
	return s.Note(comment, in.Cmd, args)
}

// Keep appends an instruction carried over unchanged, with no comment or blank
// line: used when a whole stage is replayed as written.
func (s *StageSpec) Keep(in Instruction) *StageSpec {
	n := len(s.lines)
	s.Raw("", in)
	s.lines[n].gap = false
	return s
}

// Has reports whether the stage already contains an instruction cmd.
func (s *StageSpec) Has(cmd string) bool {
	for _, l := range s.lines {
		if l.cmd == cmd {
			return true
		}
	}
	return false
}

// ExecForm renders argv as a JSON array without HTML escaping, so `&&` and `<`
// stay readable.
func ExecForm(argv []string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(argv) // []string cannot fail to encode
	return strings.TrimSpace(strings.ReplaceAll(buf.String(), `","`, `", "`))
}

// Generate renders f as Dockerfile text.
func Generate(f *File) []byte {
	var b bytes.Buffer
	if f.Syntax != "" {
		b.WriteString("# syntax=" + f.Syntax + "\n")
	}
	for _, h := range f.Header {
		writeComment(&b, h)
	}
	for i, s := range f.Stages {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		for _, c := range s.Comment {
			writeComment(&b, c)
		}
		b.WriteString("FROM " + s.From)
		if s.Name != "" && i < len(f.Stages)-1 {
			b.WriteString(" AS " + s.Name)
		}
		b.WriteByte('\n')
		for _, l := range s.lines {
			if l.gap {
				b.WriteByte('\n')
			}
			for _, c := range l.comment {
				writeComment(&b, c)
			}
			b.WriteString(l.cmd)
			if l.args != "" {
				b.WriteString(" " + l.args)
			}
			b.WriteByte('\n')
		}
	}
	return b.Bytes()
}

func writeComment(b *bytes.Buffer, c string) {
	if c == "" {
		b.WriteString("#\n")
		return
	}
	b.WriteString("# " + c + "\n")
}

func splitComment(c string) []string {
	if c == "" {
		return nil
	}
	return strings.Split(c, "\n")
}
