# test-repo

Prints a greeting.

## Usage

```
test-repo [--help] [--version] [--name=NAME] [--shout] [--quiet] [--config=FILE]
```

- `--help` / `--version`: print usage / version and exit 0.
- `--name=NAME`: who to greet (default `world`). First non-empty value wins.
- `--shout`: uppercase the output.
- `--quiet`: print nothing, exit 0.
- `--config=FILE`: read settings from FILE. Exact prefix `--config=`; the first
  non-empty value wins. An empty value counts as not given, and `--config FILE`
  (separate argument) is ignored.

## Config file

```
greeting = Good morning
```

- Lines are split on `\n`. Surrounding whitespace is trimmed (so CRLF works).
  Blank lines are ignored. No line length limit; no trailing newline needed.
- Every other line is `key=value`, split at the first `=`; key and value are
  trimmed. Keys are case-sensitive. Values are literal (may contain `=`,
  spaces, `%s`).
- Comments are not supported: a line starting with `#` is an error.
- A UTF-8 BOM is not stripped; it becomes part of the first key (unknown key).

Keys:

- `greeting`: the greeting word. Empty or absent means `Hello`.
- `name`: who to greet. Empty or absent means it is not set.
  Precedence: first non-empty `--name=` > non-empty `name` key > `world`.
  A valid `--name` does not skip the file: it is still read and validated.

The file is read only when a greeting will print: `--help`, `--version` and
`--quiet` never open it.

## Errors

Every error prints nothing on stdout, exactly one line on stderr
(`test-repo: config FILE: reason`), and exits 1. The message never echoes file
contents.

- File missing, unreadable, or a directory: the OS error.
- A line with no `=`: `line N`.
- An empty key: `line N`.
- An unknown key: `line N`.
- A repeated key (even with equal or empty values): `line N`, the later line.

## Examples

```
$ printf 'greeting=Hi\n' > c.cfg
$ test-repo --config=c.cfg            # stdout: Hi, world   exit 0
$ test-repo --config=c.cfg --name=Ann # stdout: Hi, Ann     exit 0
$ test-repo --config=c.cfg --shout    # stdout: HI, WORLD   exit 0
$ printf 'greeting=Hi\nname=Ann\n' > n.cfg
$ test-repo --config=n.cfg            # stdout: Hi, Ann     exit 0
$ test-repo --config=n.cfg --name=Bob # stdout: Hi, Bob     exit 0
$ test-repo --config=nope             # stderr: test-repo: config nope: no such file or directory   exit 1
$ test-repo --config=nope --help      # usage, exit 0 (file not opened)
```
