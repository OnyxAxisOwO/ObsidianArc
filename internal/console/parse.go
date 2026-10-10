package console

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Tokenize splits a line into words, the way a shell would for the subset
// the console needs: single quotes are literal (no escapes inside them),
// double quotes allow \" and \\ to escape themselves, and a backslash
// outside any quote escapes the very next character. Any other run of
// unicode whitespace separates words — not just the space bar, since the
// web terminal's Shift+Enter can paste a multi-line `setting set` in one
// submission.
func Tokenize(line string) ([]string, error) {
	var tokens []string
	var cur strings.Builder
	hasToken := false

	runes := []rune(line)
	i := 0
	for i < len(runes) {
		ch := runes[i]
		switch {
		case unicode.IsSpace(ch):
			if hasToken {
				tokens = append(tokens, cur.String())
				cur.Reset()
				hasToken = false
			}
			i++

		case ch == '\'':
			hasToken = true
			i++
			start := i
			for i < len(runes) && runes[i] != '\'' {
				i++
			}
			if i >= len(runes) {
				return nil, fmt.Errorf("console: unterminated ' quote")
			}
			cur.WriteString(string(runes[start:i]))
			i++ // skip the closing '

		case ch == '"':
			hasToken = true
			i++
			for i < len(runes) && runes[i] != '"' {
				if runes[i] == '\\' && i+1 < len(runes) && (runes[i+1] == '"' || runes[i+1] == '\\') {
					cur.WriteRune(runes[i+1])
					i += 2
					continue
				}
				cur.WriteRune(runes[i])
				i++
			}
			if i >= len(runes) {
				return nil, fmt.Errorf(`console: unterminated " quote`)
			}
			i++ // skip the closing "

		case ch == '\\' && i+1 < len(runes):
			hasToken = true
			cur.WriteRune(runes[i+1])
			i += 2

		default:
			hasToken = true
			cur.WriteRune(ch)
			i++
		}
	}
	if hasToken {
		tokens = append(tokens, cur.String())
	}
	return tokens, nil
}

// ParsedArgs is what ParseFlags separated a token stream into.
type ParsedArgs struct {
	Args []string
	// Flags maps a flag's canonical long name, without leading dashes, to
	// its raw value — "true" for a boolean flag given bare. -h/--help,
	// --json and -y/--yes are never in here; they are pulled out into
	// their own fields because every command answers to them, declared or
	// not (see registry.go's Flag doc).
	Flags map[string]string
	Help  bool
	JSON  bool
	Yes   bool

	// loose are the tokens that followed a bare boolean and were not taken
	// as its value, so they stand in Args. Whether one of them is a stray
	// value or a real argument depends on the command's declared Args, which
	// only refuseLooseValues is given.
	loose []looseValue
}

// looseValue is one token of that kind: the flag it followed, the token, and
// the index it holds in Args.
type looseValue struct {
	flag  string
	value string
	at    int
}

// ParseFlags walks tokens against a command's declared flags: "--flag
// value" and "--flag=value" for a value-taking flag (Value != ""), a bare
// "--flag" or "-x" for a boolean one, "--" to stop flag parsing entirely
// (everything after it is positional, however it looks), and an error for
// anything starting with "-" that matches no declared flag and is not one
// of the three universal ones above.
//
// A value is checked against what its flag's placeholder says it is (see
// valueKind) before any command runs, so a Run never sees "9O" read as 0.
// A bare boolean takes the token after it when that token is a boolean word
// in any case, and refuses a yes/no spelling that ParseBool does not take.
// --yes and -y are read the same way, because the confirmation is the one
// flag a destructive command depends on (see setYes).
func ParseFlags(tokens []string, flags []Flag) (ParsedArgs, error) {
	parsed := ParsedArgs{Flags: map[string]string{}}

	byLong := make(map[string]Flag, len(flags))
	byShort := make(map[string]Flag, len(flags))
	for _, f := range flags {
		byLong[normalizeFlagName(f.Name)] = f
		if f.Short != "" {
			byShort[normalizeFlagName(f.Short)] = f
		}
	}

	positionalOnly := false
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]

		if positionalOnly {
			parsed.Args = append(parsed.Args, tok)
			continue
		}

		switch {
		case tok == "--":
			positionalOnly = true

		case tok == "-h" || tok == "--help":
			parsed.Help = true

		case tok == "--json":
			parsed.JSON = true

		case tok == "-y" || tok == "--yes":
			next, err := parsed.setYes("", false, tokens, i)
			if err != nil {
				return ParsedArgs{}, err
			}
			i = next

		case strings.HasPrefix(tok, "--yes="):
			next, err := parsed.setYes(strings.TrimPrefix(tok, "--yes="), true, tokens, i)
			if err != nil {
				return ParsedArgs{}, err
			}
			i = next

		case strings.HasPrefix(tok, "--") && tok != "--":
			name, value, hasEq := strings.Cut(tok[2:], "=")
			f, ok := byLong[name]
			if !ok {
				return ParsedArgs{}, fmt.Errorf("console: unknown flag --%s", name)
			}
			next, err := parsed.set(f, value, hasEq, tokens, i)
			if err != nil {
				return ParsedArgs{}, err
			}
			i = next

		case strings.HasPrefix(tok, "-") && tok != "-":
			f, ok := byShort[tok[1:]]
			if !ok {
				return ParsedArgs{}, fmt.Errorf("console: unknown flag %s", tok)
			}
			next, err := parsed.set(f, "", false, tokens, i)
			if err != nil {
				return ParsedArgs{}, err
			}
			i = next

		default:
			parsed.Args = append(parsed.Args, tok)
		}
	}

	return parsed, nil
}

// set records f, which the parser met at tokens[i], and returns the index of
// the last token it used. explicit is the text after "=" when hasEq is set.
func (p *ParsedArgs) set(f Flag, explicit string, hasEq bool, tokens []string, i int) (int, error) {
	value, last, err := p.valueOf(f, explicit, hasEq, tokens, i)
	if err != nil {
		return i, err
	}
	p.Flags[normalizeFlagName(f.Name)] = value
	return last, nil
}

// yesFlag is the confirmation every destructive command waits for. It is
// declared here rather than on each command, so that no command can forget it,
// and it is read the way a declared bare boolean is (see setYes).
var yesFlag = Flag{Name: "--yes"}

// setYes records the confirmation met at tokens[i], as --yes, -y or
// --yes=VALUE, and returns the index of the last token it used.
//
// It is read as a declared bare boolean is. A parser that took --yes as a plain
// switch would leave a following "false" behind as an argument and run the
// command as confirmed, so "--yes false" must withhold the confirmation, and
// "--yes no" must be refused rather than read as on.
func (p *ParsedArgs) setYes(explicit string, hasEq bool, tokens []string, i int) (int, error) {
	value, last, err := p.valueOf(yesFlag, explicit, hasEq, tokens, i)
	if err != nil {
		return i, err
	}
	// valueOf has already checked value against the boolean rule.
	p.Yes, _ = parseBoolValue(value)
	return last, nil
}

// valueOf works out the value f takes from the line, where the parser met it at
// tokens[i], and the index of the last token that value used. explicit is the
// text after "=" when hasEq is set.
func (p *ParsedArgs) valueOf(f Flag, explicit string, hasEq bool, tokens []string, i int) (string, int, error) {
	if f.Value != "" {
		value := explicit
		if !hasEq {
			if i+1 >= len(tokens) {
				return "", i, fmt.Errorf("console: flag %s requires a value", f.Name)
			}
			i++
			value = tokens[i]
		}
		if err := checkValue(f, value); err != nil {
			return "", i, err
		}
		return value, i, nil
	}

	// "=" always names the value, so it is checked like any other. A bare
	// boolean takes the next token only when that token is a boolean word:
	// most of the time the next token is an argument that merely follows the
	// flag, and taking it would lose the argument.
	if hasEq {
		if err := checkValue(f, explicit); err != nil {
			return "", i, err
		}
		return explicit, i, nil
	}
	if i+1 < len(tokens) {
		next := tokens[i+1]
		if checkValue(f, next) == nil {
			return next, i + 1, nil
		}
		// The spellings people reach for that ParseBool does not take. Left
		// as a loose argument, "--trusted no" would switch trust on and say
		// nothing about the word it ignored.
		if isYesOrNoWord(next) {
			return "", i, valueError(f.Name, next, "true or false")
		}
		if isPositional(next) {
			p.loose = append(p.loose, looseValue{flag: f.Name, value: next, at: len(p.Args)})
		}
	}
	return "true", i, nil
}

// refuseLooseValues is the command-level half of the bare-boolean rule. A
// token that followed a bare boolean, and is not one of the command's
// declared arguments, was almost certainly the flag's value: the flag reads
// as on and the command would ignore the word. The token is refused as the
// value it was meant to be, rather than run with the word thrown away.
func refuseLooseValues(cmd *Command, parsed ParsedArgs) error {
	for _, v := range parsed.loose {
		if v.at >= len(cmd.Args) {
			return valueError(v.flag, v.value, "true or false")
		}
	}
	return nil
}

// valueKind is what a Flag.Value placeholder says a flag's value must be. The
// placeholder is already the word help and the spec print for the flag, so it
// is the declaration; a second table here would be one more thing to forget.
type valueKind int

const (
	kindText valueKind = iota
	kindBool
	kindInt
	kindFloat
	kindDuration
)

// placeholderKind maps a placeholder to its kind. An empty placeholder is a
// bare boolean, which has no value of its own and so is only checked when the
// user writes one with "=". Any placeholder not listed is text, and the
// command that declares it checks it.
func placeholderKind(placeholder string) valueKind {
	switch placeholder {
	case "", "BOOL":
		return kindBool
	case "N", "D", "PORT", "MS":
		return kindInt
	case "F":
		return kindFloat
	case "DURATION":
		return kindDuration
	}
	return kindText
}

// checkValue refuses a value that f's placeholder says it cannot be.
func checkValue(f Flag, value string) error {
	switch placeholderKind(f.Value) {
	case kindBool:
		if _, err := parseBoolValue(value); err != nil {
			return valueError(f.Name, value, "true or false")
		}
	case kindInt:
		if _, err := strconv.ParseInt(value, 10, 64); err != nil {
			return valueError(f.Name, value, "an integer")
		}
	case kindFloat:
		n, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return valueError(f.Name, value, "a number")
		}
	case kindDuration:
		if _, err := time.ParseDuration(value); err != nil {
			return valueError(f.Name, value, "a duration such as 30s or 5m")
		}
	}
	return nil
}

// parseBoolValue is strconv.ParseBool in any case, because that is how people
// type it: --enabled TRUE is as much a true as --enabled true. Commands read
// their booleans through here too, so a spelling accepted at the parser is
// read the same way by the command.
func parseBoolValue(raw string) (bool, error) {
	return strconv.ParseBool(strings.ToLower(raw))
}

// isYesOrNoWord reports the spellings of yes and no that ParseBool does not
// take, in any case.
func isYesOrNoWord(raw string) bool {
	switch strings.ToLower(raw) {
	case "yes", "no", "on", "off", "y", "n":
		return true
	}
	return false
}

// isPositional reports whether a token is read as an argument rather than as
// a flag. A lone "-" is an argument by convention, as stdin is.
func isPositional(tok string) bool {
	return !strings.HasPrefix(tok, "-") || tok == "-"
}

// valueError is the message for a value that does not fit its flag. It names
// the flag as declared and quotes the value, so a typo such as the letter O
// for a zero is visible in the message rather than inferred from its effect.
func valueError(flag, value, want string) error {
	return fmt.Errorf("%s: expected %s, got %q", flag, want, value)
}
